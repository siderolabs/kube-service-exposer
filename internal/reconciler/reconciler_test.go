// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package reconciler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/siderolabs/kube-service-exposer/internal/reconciler"
)

type mockClientProvider struct {
	objects []client.Object
}

func (m *mockClientProvider) GetClient() client.Client {
	return fake.NewClientBuilder().WithObjects(m.objects...).Build()
}

type mockServiceHandler struct {
	handles []*corev1.Service
	deletes []string
}

func (m *mockServiceHandler) Handle(svc *corev1.Service) error {
	m.handles = append(m.handles, svc)

	return nil
}

func (m *mockServiceHandler) HandleDelete(svcName string) error {
	m.deletes = append(m.deletes, svcName)

	return nil
}

func TestReconcilerCreate(t *testing.T) {
	t.Parallel()

	_, err := reconciler.New(nil, &mockServiceHandler{})
	assert.ErrorContains(t, err, "clientProvider must not be nil")

	_, err = reconciler.New(&mockClientProvider{}, nil)
	assert.ErrorContains(t, err, "serviceHandler must not be nil")

	rec, err := reconciler.New(&mockClientProvider{}, &mockServiceHandler{})
	assert.NoError(t, err)
	assert.NotNil(t, rec)
}

func TestReconcilerReconcile(t *testing.T) {
	t.Parallel()

	clientProvider := &mockClientProvider{
		objects: []client.Object{
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testname",
					Namespace: "testns",
				},
			},
		},
	}

	serviceHandler := &mockServiceHandler{}

	rec, err := reconciler.New(clientProvider, serviceHandler)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3)
	defer cancel()

	_, err = rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "testname",
			Namespace: "testns",
		},
	})
	assert.NoError(t, err)

	assert.Len(t, serviceHandler.deletes, 0)

	assert.Equal(t, serviceHandler.handles[0].Name, "testname")
	assert.Equal(t, serviceHandler.handles[0].Namespace, "testns")

	_, err = rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "non-existent",
			Namespace: "testns",
		},
	})
	assert.NoError(t, err)

	assert.Equal(t, serviceHandler.deletes[0], "non-existent.testns")
	assert.Len(t, serviceHandler.handles, 1)
}
