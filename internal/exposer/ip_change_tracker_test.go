// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/siderolabs/kube-service-exposer/internal/exposer"
)

type mockSetRefresher struct {
	changed bool
}

func (m *mockSetRefresher) RefreshChanged() (bool, error) {
	return m.changed, nil
}

func TestIPChangeTrackerRun(t *testing.T) {
	t.Parallel()

	cl := fake.NewClientBuilder().WithObjects(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "svc-a",
				Namespace: "testns",
			},
		},
	).Build()

	serviceHandler := &mockSyncServiceHandler{
		handled: make(chan []corev1.Service, 1),
	}

	tracker, err := exposer.NewIPChangeTracker(
		&mockSetRefresher{
			changed: true,
		},
		&mockSyncClientProvider{client: cl},
		serviceHandler,
		10*time.Millisecond,
		zaptest.NewLogger(t),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)

	go func() {
		runErrCh <- tracker.Run(ctx)
	}()

	var handledServices []corev1.Service

	select {
	case handledServices = <-serviceHandler.handled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for IP tracker to handle services")
	}

	assert.Len(t, handledServices, 1)
	assert.Equal(t, "testns/svc-a", handledServices[0].Namespace+"/"+handledServices[0].Name)

	cancel()

	select {
	case err = <-runErrCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for IP tracker to stop")
	}
}

func TestIPChangeTrackerRunSkipsUnchangedIPSet(t *testing.T) {
	t.Parallel()

	cl := fake.NewClientBuilder().WithObjects(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "svc-a",
				Namespace: "testns",
			},
		},
	).Build()

	serviceHandler := &mockSyncServiceHandler{
		handled: make(chan []corev1.Service, 1),
	}

	tracker, err := exposer.NewIPChangeTracker(
		&mockSetRefresher{
			changed: false,
		},
		&mockSyncClientProvider{client: cl},
		serviceHandler,
		10*time.Millisecond,
		zaptest.NewLogger(t),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)

	go func() {
		runErrCh <- tracker.Run(ctx)
	}()

	select {
	case handledServices := <-serviceHandler.handled:
		t.Fatalf("expected no handled services, got %d", len(handledServices))
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err = <-runErrCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for IP tracker to stop")
	}
}
