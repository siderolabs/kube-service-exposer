// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/siderolabs/kube-service-exposer/internal/service"
)

type addEntry struct {
	svcName  string
	hostPort int
	svcPort  int
}

type mockIPMapper struct {
	addEntries    []addEntry
	removeEntries []string
}

func (m *mockIPMapper) Add(svcName string, hostPort, svcPort int) error {
	m.addEntries = append(m.addEntries, addEntry{
		svcName:  svcName,
		hostPort: hostPort,
		svcPort:  svcPort,
	})

	return nil
}

func (m *mockIPMapper) Remove(svcName string) {
	m.removeEntries = append(m.removeEntries, svcName)
}

func TestHandlerCreate(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	_, err := service.NewHandler("test", nil, nil, logger)
	assert.ErrorContains(t, err, "must not be nil")

	_, err = service.NewHandler("", &mockIPMapper{}, nil, logger)
	assert.ErrorContains(t, err, "invalid annotation key")

	_, err = service.NewHandler("invalid key 1", &mockIPMapper{}, nil, logger)
	assert.ErrorContains(t, err, "invalid annotation key")

	handler, err := service.NewHandler("valid-key", &mockIPMapper{}, nil, logger)
	require.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestHandlerHandle(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	mapper := mockIPMapper{}

	handler, err := service.NewHandler("test", &mapper, nil, logger)
	require.NoError(t, err)

	assert.ErrorContains(t, handler.Handle(nil), "must not be nil")

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testname",
			Namespace: "testns",
		},
	}

	require.NoError(t, handler.Handle(svc))

	// no mappings expected, as no ports are defined
	assert.Empty(t, mapper.addEntries)

	tcpPort1 := corev1.ServicePort{
		Name:     "tcp-1",
		Port:     8080,
		Protocol: corev1.ProtocolTCP,
	}

	tcpPort2 := corev1.ServicePort{
		Name:     "tcp-2",
		Port:     8081,
		Protocol: corev1.ProtocolTCP,
	}

	udpPort := corev1.ServicePort{
		Name:     "udp",
		Port:     8082,
		Protocol: corev1.ProtocolUDP,
	}

	svc.Spec.Ports = []corev1.ServicePort{tcpPort1}

	require.NoError(t, handler.Handle(svc))

	// no mappings expected, as the service does not have the annotation
	assert.Empty(t, mapper.addEntries)

	svc.Annotations = map[string]string{
		"test": "test",
	}

	// invalid annotation value - cannot be parsed to int
	assert.ErrorContains(t, handler.Handle(svc), "invalid host port")

	svc.Annotations["test"] = "12345"
	svc.Spec.Ports = []corev1.ServicePort{udpPort}

	require.NoError(t, handler.Handle(svc))

	// no mappings expected, as the service does not contain a TCP port
	assert.Empty(t, mapper.addEntries)

	svc.Spec.Ports = []corev1.ServicePort{tcpPort1, udpPort, tcpPort2}

	require.NoError(t, handler.Handle(svc))

	// 1 mapping expected for the 1st tcp port
	assert.Len(t, mapper.addEntries, 1)
	assert.Equal(t, "testname.testns", mapper.addEntries[0].svcName)
	assert.Equal(t, 12345, mapper.addEntries[0].hostPort)
	assert.Equal(t, 8080, mapper.addEntries[0].svcPort)

	// update annotations
	svc.Annotations["test"] = "12346"

	require.NoError(t, handler.Handle(svc))

	// mapping update expected
	assert.Len(t, mapper.addEntries, 2)
	assert.Equal(t, "testname.testns", mapper.addEntries[1].svcName)
	assert.Equal(t, 12346, mapper.addEntries[1].hostPort)
	assert.Equal(t, 8080, mapper.addEntries[1].svcPort)

	// remove annotation
	delete(svc.Annotations, "test")

	removeEntryCount := len(mapper.removeEntries)

	require.NoError(t, handler.Handle(svc))

	// mapping removal expected
	assert.Len(t, mapper.removeEntries, removeEntryCount+1)
	assert.Equal(t, "testname.testns", mapper.removeEntries[removeEntryCount])

	// add annotation again
	svc.Annotations["test"] = "12347"

	require.NoError(t, handler.Handle(svc))

	// remove tcp ports
	svc.Spec.Ports = []corev1.ServicePort{udpPort}

	removeEntryCount = len(mapper.removeEntries)

	require.NoError(t, handler.Handle(svc))

	// mapping removal expected
	assert.Len(t, mapper.removeEntries, removeEntryCount+1)
	assert.Equal(t, "testname.testns", mapper.removeEntries[removeEntryCount])
}

func TestHandlerHandleDelete(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	mapper := mockIPMapper{}

	handler, err := service.NewHandler("test", &mapper, nil, logger)
	require.NoError(t, err)

	assert.ErrorContains(t, handler.HandleDelete(""), "must not be empty")

	require.NoError(t, handler.HandleDelete("testname.testns"))

	assert.Len(t, mapper.removeEntries, 1)
	assert.Equal(t, "testname.testns", mapper.removeEntries[0])
}

func TestHandlerHandleIgnoreDisallowed(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	mapper := mockIPMapper{}

	handler, err := service.NewHandler("test", &mapper, []string{"0-1024", "10250", "50000"}, logger)
	require.NoError(t, err)

	assert.ErrorContains(t, handler.Handle(nil), "must not be nil")

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
				{
					Name:     "tcp-1",
					Port:     8080,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}

	require.NoError(t, handler.Handle(svc))

	// 1 mapping expected for the 1st tcp port
	assert.Len(t, mapper.addEntries, 1)
	assert.Equal(t, "testname.testns", mapper.addEntries[0].svcName)
	assert.Equal(t, 12345, mapper.addEntries[0].hostPort)
	assert.Equal(t, 8080, mapper.addEntries[0].svcPort)

	// update annotations
	svc.Annotations["test"] = "1023"

	require.NoError(t, handler.Handle(svc))

	// mapping removal expected
	assert.Len(t, mapper.removeEntries, 1)

	// no new mappings expected
	assert.Len(t, mapper.addEntries, 1)

	// update annotations
	svc.Annotations["test"] = "50000"

	require.NoError(t, handler.Handle(svc))

	// no new mappings expected
	assert.Len(t, mapper.addEntries, 1)

	// update annotations
	svc.Annotations["test"] = "50002"

	require.NoError(t, handler.Handle(svc))

	// 1 mapping expected for the 1st tcp port
	assert.Len(t, mapper.addEntries, 2)
}
