// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip_test

import (
	"errors"
	"iter"
	"slices"
	"testing"

	"github.com/siderolabs/gen/xslices"
	"github.com/siderolabs/go-loadbalancer/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/types"

	"github.com/siderolabs/kube-service-exposer/internal/ip"
)

type mockIPSetProvider struct {
	getErr error
	ips    []string
}

func (m *mockIPSetProvider) Get() (map[string]struct{}, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}

	return xslices.ToSet(m.ips), nil
}

func (m *mockIPSetProvider) Refresh() (map[string]struct{}, error) {
	return m.Get()
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

func key(name, ns string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: ns}
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

func TestMapperReconcile_AddsAndRoutes(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: []string{"192.168.2.42", "172.20.0.42"}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc1", "ns1"),
		Mappings:   []ip.Mapping{{HostPort: 12345, ServicePort: 80}},
	}))

	require.Len(t, lbs.lbs, 1)
	assert.True(t, lbs.lbs[0].started)
	assert.False(t, lbs.lbs[0].closed)
	assert.Equal(t, []string{"svc1.ns1:80"}, lbs.lbs[0].routes["192.168.2.42:12345"])
	assert.Equal(t, []string{"svc1.ns1:80"}, lbs.lbs[0].routes["172.20.0.42:12345"])
}

func TestMapperReconcile_IsIdempotent(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: []string{"10.0.0.1"}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	set := ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}

	require.NoError(t, mapper.Reconcile(set))
	require.NoError(t, mapper.Reconcile(set))
	require.NoError(t, mapper.Reconcile(set))

	// no churn: only one LB ever created.
	assert.Len(t, lbs.lbs, 1)
	assert.False(t, lbs.lbs[0].closed)
}

func TestMapperReconcile_ChangeRecyclesLB(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: []string{"10.0.0.1"}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))

	// change the service port: must close the old LB and create a new one.
	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 8080}},
	}))

	require.Len(t, lbs.lbs, 2)
	assert.True(t, lbs.lbs[0].closed)
	assert.True(t, lbs.lbs[1].started)
	assert.Equal(t, []string{"svc.ns:8080"}, lbs.lbs[1].routes["10.0.0.1:30080"])
}

func TestMapperReconcile_RemovesEntriesNoLongerDesired(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: []string{"10.0.0.1"}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings: []ip.Mapping{
			{HostPort: 30080, ServicePort: 80},
			{HostPort: 30443, ServicePort: 443},
		},
	}))
	require.Len(t, lbs.lbs, 2)

	// drop one of the two mappings.
	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))

	closedCount := 0

	for _, lb := range lbs.lbs {
		if lb.closed {
			closedCount++
		}
	}

	assert.Equal(t, 1, closedCount)

	// empty set removes everything for the service.
	require.NoError(t, mapper.Reconcile(ip.MappingSet{ServiceKey: key("svc", "ns")}))

	for _, lb := range lbs.lbs {
		assert.True(t, lb.closed)
	}
}

func TestMapperReconcile_CrossServiceCollision(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: []string{"10.0.0.1"}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc1", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))

	err = mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc2", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 8080}},
	})
	assert.ErrorContains(t, err, "already registered to another service")

	// svc1's mapping is intact.
	assert.Len(t, lbs.lbs, 1)
	assert.False(t, lbs.lbs[0].closed)
}

func TestMapperReconcile_EmptyIPSetIsPending(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: nil}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	// no host IPs match: mapping is recorded but no LB is created.
	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))
	assert.Empty(t, lbs.lbs, "no load balancer should be created when no IPs match")

	// IPs become available: a subsequent Reconcile creates the LB.
	provider.ips = []string{"10.0.0.1"}

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))
	require.Len(t, lbs.lbs, 1)
	assert.True(t, lbs.lbs[0].started)
	assert.Equal(t, []string{"svc.ns:80"}, lbs.lbs[0].routes["10.0.0.1:30080"])

	// IPs disappear again: existing LB is torn down, mapping becomes pending.
	provider.ips = nil

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))
	assert.True(t, lbs.lbs[0].closed)
	assert.Len(t, lbs.lbs, 1, "no new LB created for the pending mapping")

	// Pending mapping can still be removed cleanly without a real LB attached.
	require.NoError(t, mapper.Reconcile(ip.MappingSet{ServiceKey: key("svc", "ns")}))
}

func TestMapperReconcile_PureRemoveDoesNotNeedIPs(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: []string{"10.0.0.1"}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))

	// IP discovery breaks. Removal must still succeed.
	provider.getErr = errors.New("interfaces unavailable")
	provider.ips = nil

	require.NoError(t, mapper.Reconcile(ip.MappingSet{ServiceKey: key("svc", "ns")}))
	assert.True(t, lbs.lbs[0].closed)

	// Re-adding still requires IPs and surfaces the error.
	err = mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("svc", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	})
	assert.ErrorContains(t, err, "interfaces unavailable")
}

type countingProvider struct {
	mockIPSetProvider

	refreshes int
}

func (c *countingProvider) Refresh() (map[string]struct{}, error) {
	c.refreshes++

	return c.mockIPSetProvider.Refresh()
}

func TestMapperKnownServicesAndRefreshIPSet(t *testing.T) {
	t.Parallel()

	provider := &countingProvider{mockIPSetProvider: mockIPSetProvider{ips: []string{"10.0.0.1"}}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	assert.Empty(t, mapper.KnownServices())

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("b", "ns2"),
		Mappings:   []ip.Mapping{{HostPort: 30443, ServicePort: 443}},
	}))
	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("a", "ns1"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))

	// sorted: ns1/a before ns2/b.
	assert.Equal(t, []types.NamespacedName{key("a", "ns1"), key("b", "ns2")}, mapper.KnownServices())

	require.NoError(t, mapper.RefreshIPSet())
	assert.Equal(t, 1, provider.refreshes)

	// removing the last mapping for a service drops it from KnownServices.
	require.NoError(t, mapper.Reconcile(ip.MappingSet{ServiceKey: key("a", "ns1")}))
	assert.Equal(t, []types.NamespacedName{key("b", "ns2")}, mapper.KnownServices())
}

func TestMapperClose(t *testing.T) {
	t.Parallel()

	provider := &mockIPSetProvider{ips: []string{"10.0.0.1"}}
	lbs := &mockLoadBalancerProvider{}

	mapper, err := ip.NewMapper(provider, lbs, zaptest.NewLogger(t))
	require.NoError(t, err)

	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("a", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30080, ServicePort: 80}},
	}))
	require.NoError(t, mapper.Reconcile(ip.MappingSet{
		ServiceKey: key("b", "ns"),
		Mappings:   []ip.Mapping{{HostPort: 30443, ServicePort: 443}},
	}))

	mapper.Close()

	for _, lb := range lbs.lbs {
		assert.True(t, lb.closed)
	}

	// idempotent.
	mapper.Close()
}
