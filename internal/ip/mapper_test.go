// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip_test

import (
	"iter"
	"slices"
	"testing"

	"github.com/siderolabs/gen/xslices"
	"github.com/siderolabs/go-loadbalancer/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
)

type mockIPSetProvider struct {
	ips []string
}

func (m *mockIPSetProvider) Get() (map[string]struct{}, error) {
	return xslices.ToSet(m.ips), nil
}

type mockLoadBalancer struct {
	routes  map[string][]string
	started bool
	closed  bool
}

func (m *mockLoadBalancer) Wait() error {
	return nil
}

func (m *mockLoadBalancer) AddRoute(ipPort string, upstreamAddrs iter.Seq[string], _ ...upstream.ListOption) error {
	if m.routes == nil {
		m.routes = make(map[string][]string)
	}

	m.routes[ipPort] = slices.Collect(upstreamAddrs)

	return nil
}

func (m *mockLoadBalancer) Start() error {
	m.started = true

	return nil
}

func (m *mockLoadBalancer) Close() error {
	m.closed = true

	return nil
}

type mockLoadBalancerProvider struct {
	lbs []*mockLoadBalancer
}

func (m *mockLoadBalancerProvider) New(_ *zap.Logger) (ip.LoadBalancer, error) {
	lb := &mockLoadBalancer{}

	m.lbs = append(m.lbs, lb)

	return lb, nil
}

func TestMapperCreate(t *testing.T) {
	t.Parallel()

	logger := zaptest.NewLogger(t)

	_, err := ip.NewMapper(nil, nil, logger)
	assert.ErrorContains(t, err, "must not be nil")

	mapper, err := ip.NewMapper(&mockIPSetProvider{}, &ip.TCPLoadBalancerProvider{}, logger)
	require.NoError(t, err)

	assert.NotNil(t, mapper)
}

func TestMapper(t *testing.T) {
	t.Parallel()

	ipSetProvider := &mockIPSetProvider{
		ips: []string{"192.168.2.42", "172.20.0.42"},
	}

	lbProvider := &mockLoadBalancerProvider{}

	logger := zaptest.NewLogger(t)

	mapper, err := ip.NewMapper(ipSetProvider, lbProvider, logger)
	require.NoError(t, err)

	require.NoError(t, mapper.Add(ip.Mapping{ServiceKey: client.ObjectKey{Namespace: "ns1", Name: "svc1"}, HostPort: 12345, SvcPort: 80}))

	assert.Len(t, lbProvider.lbs, 1)
	assert.Len(t, lbProvider.lbs[0].routes, 2)
	assert.Equal(t, []string{"svc1.ns1:80"}, lbProvider.lbs[0].routes["192.168.2.42:12345"])
	assert.Equal(t, []string{"svc1.ns1:80"}, lbProvider.lbs[0].routes["172.20.0.42:12345"])

	assert.ErrorContains(t, mapper.Add(ip.Mapping{ServiceKey: client.ObjectKey{Namespace: "ns2", Name: "svc2"}, HostPort: 12345, SvcPort: 80}), "already registered to another service")

	require.NoError(t, mapper.Add(ip.Mapping{ServiceKey: client.ObjectKey{Namespace: "ns2", Name: "svc2"}, HostPort: 12346, SvcPort: 8080}))

	assert.Len(t, lbProvider.lbs, 2)
	assert.Len(t, lbProvider.lbs[1].routes, 2)
	assert.Equal(t, []string{"svc2.ns2:8080"}, lbProvider.lbs[1].routes["192.168.2.42:12346"])
	assert.Equal(t, []string{"svc2.ns2:8080"}, lbProvider.lbs[1].routes["172.20.0.42:12346"])

	require.NoError(t, mapper.Add(ip.Mapping{ServiceKey: client.ObjectKey{Namespace: "ns2", Name: "svc2"}, HostPort: 12347, SvcPort: 8081}))

	assert.Len(t, lbProvider.lbs, 3)
	assert.Len(t, lbProvider.lbs[2].routes, 2)
	assert.Equal(t, []string{"svc2.ns2:8081"}, lbProvider.lbs[2].routes["192.168.2.42:12347"])
	assert.Equal(t, []string{"svc2.ns2:8081"}, lbProvider.lbs[2].routes["172.20.0.42:12347"])

	assert.True(t, lbProvider.lbs[0].started)
	assert.False(t, lbProvider.lbs[0].closed)

	mapper.Remove(client.ObjectKey{Namespace: "ns2", Name: "svc2"})

	assert.True(t, lbProvider.lbs[2].closed)
}

func TestMapperSyncReplacesStaleOwner(t *testing.T) {
	t.Parallel()

	ipSetProvider := &mockIPSetProvider{
		ips: []string{"192.168.2.42"},
	}

	lbProvider := &mockLoadBalancerProvider{}

	logger := zaptest.NewLogger(t)

	mapper, err := ip.NewMapper(ipSetProvider, lbProvider, logger)
	require.NoError(t, err)

	svcA := client.ObjectKey{Namespace: "ns1", Name: "svc-a"}
	svcB := client.ObjectKey{Namespace: "ns1", Name: "svc-b"}

	require.NoError(t, mapper.Add(ip.Mapping{
		ServiceKey: svcA,
		HostPort:   12345,
		SvcPort:    80,
	}))

	require.NoError(t, mapper.Sync([]ip.Mapping{
		{
			ServiceKey: svcB,
			HostPort:   12345,
			SvcPort:    8080,
		},
	}))

	require.Len(t, lbProvider.lbs, 2)

	assert.True(t, lbProvider.lbs[0].closed)
	assert.True(t, lbProvider.lbs[1].started)
	assert.Equal(t, []string{"svc-b.ns1:8080"}, lbProvider.lbs[1].routes["192.168.2.42:12345"])
}
