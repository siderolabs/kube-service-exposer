// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package exposer_test

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockSyncClientProvider struct {
	client client.Client
}

func (m *mockSyncClientProvider) GetClient() client.Client {
	return m.client
}

type mockSyncServiceHandler struct {
	handled chan []corev1.Service
}

func (m *mockSyncServiceHandler) HandleAll(services []corev1.Service) error {
	servicesCopy := append([]corev1.Service(nil), services...)

	m.handled <- servicesCopy

	return nil
}
