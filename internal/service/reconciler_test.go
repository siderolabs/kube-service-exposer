// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
	"github.com/siderolabs/kube-service-exposer/internal/service"
)

type mockClientProvider struct {
	objects []client.Object
}

func (m *mockClientProvider) GetClient() client.Client {
	return fake.NewClientBuilder().WithObjects(m.objects...).Build()
}

type mockIPMapper struct {
	calls []ip.MappingSet

	lock sync.Mutex
}

func (m *mockIPMapper) Reconcile(set ip.MappingSet) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.calls = append(m.calls, set)

	return nil
}

func (m *mockIPMapper) Calls() []ip.MappingSet {
	m.lock.Lock()
	defer m.lock.Unlock()

	return append([]ip.MappingSet(nil), m.calls...)
}

func TestReconcilerCreate(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	_, err := service.NewReconciler("test", nil, &mockIPMapper{}, nil, logger)
	assert.ErrorContains(t, err, "clientProvider must not be nil")

	_, err = service.NewReconciler("test", &mockClientProvider{}, nil, nil, logger)
	assert.ErrorContains(t, err, "ipMapper must not be nil")

	_, err = service.NewReconciler("", &mockClientProvider{}, &mockIPMapper{}, nil, logger)
	assert.ErrorContains(t, err, "invalid annotation key")

	_, err = service.NewReconciler("invalid key 1", &mockClientProvider{}, &mockIPMapper{}, nil, logger)
	assert.ErrorContains(t, err, "invalid annotation key")

	rec, err := service.NewReconciler("valid-key", &mockClientProvider{}, &mockIPMapper{}, nil, logger)
	require.NoError(t, err)
	assert.NotNil(t, rec)
}

func TestReconcilerNotFoundRemovesMappings(t *testing.T) {
	t.Parallel()

	mapper := &mockIPMapper{}

	rec, err := service.NewReconciler("test", &mockClientProvider{}, mapper, nil, zaptest.NewLogger(t))
	require.NoError(t, err)

	_, err = rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "gone", Namespace: "ns"},
	})
	require.NoError(t, err)

	calls := mapper.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, types.NamespacedName{Name: "gone", Namespace: "ns"}, calls[0].ServiceKey)
	assert.Empty(t, calls[0].Mappings)
}

func TestReconcilerHappyPath(t *testing.T) {
	t.Parallel()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testname",
			Namespace: "testns",
			Annotations: map[string]string{
				"test": "12345",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "tcp-1", Port: 8080, Protocol: corev1.ProtocolTCP},
			},
		},
	}

	mapper := &mockIPMapper{}

	rec, err := service.NewReconciler("test", &mockClientProvider{objects: []client.Object{svc}}, mapper, nil, zaptest.NewLogger(t))
	require.NoError(t, err)

	_, err = rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "testname", Namespace: "testns"},
	})
	require.NoError(t, err)

	calls := mapper.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, types.NamespacedName{Name: "testname", Namespace: "testns"}, calls[0].ServiceKey)
	require.Len(t, calls[0].Mappings, 1)
	assert.Equal(t, ip.Mapping{HostPort: 12345, ServicePort: 8080}, calls[0].Mappings[0])
}

func TestReconcilerMultipleMappings(t *testing.T) {
	t.Parallel()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc",
			Namespace: "ns",
			Annotations: map[string]string{
				"test": "30080,30443:https,30022:22",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP},
				{Name: "ssh", Port: 22, Protocol: corev1.ProtocolTCP},
			},
		},
	}

	mapper := &mockIPMapper{}

	rec, err := service.NewReconciler("test", &mockClientProvider{objects: []client.Object{svc}}, mapper, nil, zaptest.NewLogger(t))
	require.NoError(t, err)

	_, err = rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "svc", Namespace: "ns"},
	})
	require.NoError(t, err)

	require.Len(t, mapper.Calls(), 1)
	assert.ElementsMatch(t, mapper.Calls()[0].Mappings, []ip.Mapping{
		{HostPort: 30080, ServicePort: 80},
		{HostPort: 30443, ServicePort: 443},
		{HostPort: 30022, ServicePort: 22},
	})
}

func TestReconcilerSkipsBadEntriesAndDuplicates(t *testing.T) {
	t.Parallel()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc",
			Namespace: "ns",
			Annotations: map[string]string{
				"test": "30080,not-a-port,30080:443,30022:nope,,30022",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP},
			},
		},
	}

	mapper := &mockIPMapper{}

	rec, err := service.NewReconciler("test", &mockClientProvider{objects: []client.Object{svc}}, mapper, nil, zaptest.NewLogger(t))
	require.NoError(t, err)

	_, err = rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "svc", Namespace: "ns"},
	})
	require.NoError(t, err)

	// "not-a-port" — invalid; "30080:443" — duplicate of 30080; "30022:nope" — port name not
	// found; "" — empty entry. Only 30080 (default svc port) and the second 30022 (default
	// svc port) should remain — but 30022 appears twice with different intents, with the
	// second being valid; the first 30022:nope is invalid so the second wins.
	assert.ElementsMatch(t, mapper.Calls()[0].Mappings, []ip.Mapping{
		{HostPort: 30080, ServicePort: 80},
		{HostPort: 30022, ServicePort: 80},
	})
}

func TestReconcilerIgnoresUDPPorts(t *testing.T) {
	t.Parallel()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc",
			Namespace: "ns",
			Annotations: map[string]string{
				"test": "30053:53,30080",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP},
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
			},
		},
	}

	mapper := &mockIPMapper{}

	rec, err := service.NewReconciler("test", &mockClientProvider{objects: []client.Object{svc}}, mapper, nil, zaptest.NewLogger(t))
	require.NoError(t, err)

	_, err = rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "svc", Namespace: "ns"},
	})
	require.NoError(t, err)

	// 30053:53 is rejected (UDP port not in TCP set); 30080 falls back to first TCP port (80).
	assert.ElementsMatch(t, mapper.Calls()[0].Mappings, []ip.Mapping{
		{HostPort: 30080, ServicePort: 80},
	})
}

func TestReconcilerRejectsEmptyServicePortSelector(t *testing.T) {
	t.Parallel()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc",
			Namespace: "ns",
			Annotations: map[string]string{
				"test": "30080:,30443",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				// unnamed port: the empty selector "30080:" must NOT match it.
				{Port: 80, Protocol: corev1.ProtocolTCP},
			},
		},
	}

	mapper := &mockIPMapper{}

	rec, err := service.NewReconciler("test", &mockClientProvider{objects: []client.Object{svc}}, mapper, nil, zaptest.NewLogger(t))
	require.NoError(t, err)

	_, err = rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "svc", Namespace: "ns"},
	})
	require.NoError(t, err)

	// only "30443" survives — it falls back to the first TCP port.
	assert.ElementsMatch(t, mapper.Calls()[0].Mappings, []ip.Mapping{
		{HostPort: 30443, ServicePort: 80},
	})
}

func TestReconcilerDisallowedRanges(t *testing.T) {
	t.Parallel()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc",
			Namespace: "ns",
			Annotations: map[string]string{
				"test": "1023,30080,50000",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
			},
		},
	}

	mapper := &mockIPMapper{}

	rec, err := service.NewReconciler("test", &mockClientProvider{objects: []client.Object{svc}}, mapper, []string{"0-1024", "50000"}, zaptest.NewLogger(t))
	require.NoError(t, err)

	_, err = rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "svc", Namespace: "ns"},
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, mapper.Calls()[0].Mappings, []ip.Mapping{
		{HostPort: 30080, ServicePort: 80},
	})
}
