// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip_test

import (
	"testing"

	"github.com/siderolabs/gen/xslices"
	"github.com/siderolabs/go-loadbalancer/upstream"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

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

func (m *mockLoadBalancer) AddRoute(ipPort string, upstreamAddrs []string, _ ...upstream.ListOption) error {
	if m.routes == nil {
		m.routes = make(map[string][]string)
	}

	m.routes[ipPort] = upstreamAddrs

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
	lbs []ip.LoadBalancer
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

	mapper, err := ip.NewMapper(&mockIPSetProvider{}, nil, logger)
	assert.NoError(t, err)

	assert.NotNil(t, mapper)
}

//nolint:forcetypeassert
func TestMapper(t *testing.T) {
	t.Parallel()

	ipSetProvider := &mockIPSetProvider{
		ips: []string{"192.168.2.42", "172.20.0.42"},
	}

	lbProvider := &mockLoadBalancerProvider{}

	logger := zaptest.NewLogger(t)

	mapper, err := ip.NewMapper(ipSetProvider, lbProvider, logger)
	assert.NoError(t, err)

	assert.NoError(t, mapper.Add("svc1.ns1", 12345, 80))

	assert.Len(t, lbProvider.lbs, 1)
	assert.Len(t, lbProvider.lbs[0].(*mockLoadBalancer).routes, 2)
	assert.Equal(t, []string{"svc1.ns1:80"}, lbProvider.lbs[0].(*mockLoadBalancer).routes["192.168.2.42:12345"])
	assert.Equal(t, []string{"svc1.ns1:80"}, lbProvider.lbs[0].(*mockLoadBalancer).routes["172.20.0.42:12345"])

	assert.ErrorContains(t, mapper.Add("svc2.ns2", 12345, 80), "already registered to another service")

	assert.NoError(t, mapper.Add("svc2.ns2", 12346, 8080))

	assert.Len(t, lbProvider.lbs, 2)
	assert.Len(t, lbProvider.lbs[1].(*mockLoadBalancer).routes, 2)
	assert.Equal(t, []string{"svc2.ns2:8080"}, lbProvider.lbs[1].(*mockLoadBalancer).routes["192.168.2.42:12346"])
	assert.Equal(t, []string{"svc2.ns2:8080"}, lbProvider.lbs[1].(*mockLoadBalancer).routes["172.20.0.42:12346"])

	assert.NoError(t, mapper.Add("svc2.ns2", 12347, 8081))

	assert.Len(t, lbProvider.lbs, 3)
	assert.Len(t, lbProvider.lbs[2].(*mockLoadBalancer).routes, 2)
	assert.Equal(t, []string{"svc2.ns2:8081"}, lbProvider.lbs[2].(*mockLoadBalancer).routes["192.168.2.42:12347"])
	assert.Equal(t, []string{"svc2.ns2:8081"}, lbProvider.lbs[2].(*mockLoadBalancer).routes["172.20.0.42:12347"])

	assert.True(t, lbProvider.lbs[0].(*mockLoadBalancer).started)
	assert.False(t, lbProvider.lbs[0].(*mockLoadBalancer).closed)

	mapper.Remove("svc2.ns2")

	assert.True(t, lbProvider.lbs[2].(*mockLoadBalancer).closed)
}
