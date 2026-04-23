// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/siderolabs/gen/xslices"
	"github.com/siderolabs/go-retry/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
)

type mockSetRefresher struct {
	ipSet map[string]struct{}

	lock sync.Mutex
}

func (m *mockSetRefresher) Refresh() (map[string]struct{}, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.ipSet, nil
}

type mockClientProvider struct {
	objects []client.Object
}

func (m *mockClientProvider) GetClient() client.Client {
	return fake.NewClientBuilder().WithObjects(m.objects...).Build()
}

type mockServiceHandler struct {
	handles []*corev1.Service
	errs    []error

	lock sync.Mutex
}

func (m *mockServiceHandler) Handle(svc *corev1.Service) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.handles = append(m.handles, svc)

	if len(m.errs) > 0 {
		err := m.errs[0]
		m.errs = m.errs[1:]

		return err
	}

	return nil
}

func (m *mockServiceHandler) Handles() []*corev1.Service {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.handles
}

func (m *mockServiceHandler) SetErrors(errs ...error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.errs = errs
}

func TestTrackerCreate(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	_, err := ip.NewTracker(nil, &mockClientProvider{}, &mockServiceHandler{}, 30*time.Second, nil, logger)
	assert.ErrorContains(t, err, "refresher must not be nil")

	_, err = ip.NewTracker(&mockSetRefresher{}, nil, &mockServiceHandler{}, 30*time.Second, nil, logger)
	assert.ErrorContains(t, err, "clientProvider must not be nil")

	_, err = ip.NewTracker(&mockSetRefresher{}, &mockClientProvider{}, nil, 30*time.Second, nil, logger)
	assert.ErrorContains(t, err, "serviceHandler must not be nil")

	tracker, err := ip.NewTracker(&mockSetRefresher{}, &mockClientProvider{}, &mockServiceHandler{}, 30*time.Second, nil, logger)
	require.NoError(t, err)
	assert.NotNil(t, tracker)
}

func TestTracker(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	mockClock := clock.NewMock()

	refresher := &mockSetRefresher{}

	clientProvider := &mockClientProvider{
		objects: []client.Object{
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test1",
					Namespace: "testns1",
				},
			},
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test2",
					Namespace: "testns2",
				},
			},
		},
	}

	serviceHandler := &mockServiceHandler{}

	tracker, err := ip.NewTracker(refresher, clientProvider, serviceHandler, 2*time.Second, mockClock, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return tracker.Run(ctx)
	})

	sleepWithContext(ctx, 100*time.Millisecond)

	mockClock.Add(3 * time.Second)

	// no handles expected, no changes
	assert.Empty(t, serviceHandler.Handles())

	refresher.lock.Lock()

	refresher.ipSet = map[string]struct{}{
		"192.168.2.42": {},
		"172.20.0.42":  {},
	}

	refresher.lock.Unlock()

	mockClock.Add(2 * time.Second)

	// handling expected
	err = retry.Constant(3*time.Second, retry.WithUnits(50*time.Millisecond)).Retry(func() error {
		length := len(serviceHandler.Handles())
		if length < 2 {
			return retry.ExpectedError(fmt.Errorf("not enough handles: %d", length))
		}

		return nil
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, xslices.Map(serviceHandler.Handles(), func(svc *corev1.Service) string {
		return svc.Name + "." + svc.Namespace
	}), []string{"test1.testns1", "test2.testns2"})

	mockClock.Add(2 * time.Second)

	sleepWithContext(ctx, 500*time.Millisecond)

	// no handles expected, no changes
	assert.Len(t, serviceHandler.Handles(), 2)

	cancel()

	require.NoError(t, eg.Wait())
}

func TestTrackerRetriesRefreshAfterServiceHandlerFailure(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	mockClock := clock.NewMock()

	refresher := &mockSetRefresher{}

	clientProvider := &mockClientProvider{
		objects: []client.Object{
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
			},
		},
	}

	serviceHandler := &mockServiceHandler{}
	serviceHandler.SetErrors(fmt.Errorf("handler failed"))

	tracker, err := ip.NewTracker(refresher, clientProvider, serviceHandler, 2*time.Second, mockClock, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return tracker.Run(ctx)
	})

	sleepWithContext(ctx, 100*time.Millisecond)

	refresher.lock.Lock()
	refresher.ipSet = map[string]struct{}{
		"192.168.2.42": {},
	}
	refresher.lock.Unlock()

	mockClock.Add(2 * time.Second)

	err = retry.Constant(3*time.Second, retry.WithUnits(50*time.Millisecond)).Retry(func() error {
		if length := len(serviceHandler.Handles()); length < 1 {
			return retry.ExpectedError(fmt.Errorf("not enough handles: %d", length))
		}

		return nil
	})
	require.NoError(t, err)

	mockClock.Add(2 * time.Second)

	err = retry.Constant(3*time.Second, retry.WithUnits(50*time.Millisecond)).Retry(func() error {
		if length := len(serviceHandler.Handles()); length < 2 {
			return retry.ExpectedError(fmt.Errorf("not enough handles: %d", length))
		}

		return nil
	})
	require.NoError(t, err)

	cancel()

	require.NoError(t, eg.Wait())
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	select {
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
	}
}
