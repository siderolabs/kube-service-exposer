// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/siderolabs/go-loadbalancer/upstream"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/types"
)

// SetProvider is an interface for getting a set of IP addresses.
//
// Refresh invalidates any cached value and returns a freshly fetched set; Get returns the
// cached value (or fetches it on first call).
type SetProvider interface {
	Get() (map[string]struct{}, error)
	Refresh() (map[string]struct{}, error)
}

type hostPort int

type ipSet map[string]struct{}

type portMappings map[hostPort]*portMapping

type portMapping struct {
	hostIPSet  ipSet
	lb         LoadBalancer
	serviceKey types.NamespacedName
	mapping    Mapping
}

// Mapping is one host-port-to-service-port pair.
type Mapping struct {
	HostPort    int
	ServicePort int
}

// Equal reports whether two Mappings are identical.
func (m Mapping) Equal(other Mapping) bool {
	return m.HostPort == other.HostPort && m.ServicePort == other.ServicePort
}

// String formats a Mapping as "host->service".
func (m Mapping) String() string {
	return fmt.Sprintf("%d->%d", m.HostPort, m.ServicePort)
}

// MappingSet is the desired set of port mappings for a single Service.
type MappingSet struct {
	ServiceKey types.NamespacedName
	Mappings   []Mapping
}

// Mapper maps IP addresses on the host to Kubernetes Service resources.
type Mapper struct {
	ipSetProvider          SetProvider
	loadBalancerController LoadBalancerProvider
	hostPortToMapping      map[hostPort]*portMapping
	serviceKeyToMappings   map[types.NamespacedName]portMappings
	logger                 *zap.Logger
	lock                   sync.Mutex
}

// NewMapper returns a new Mapper.
func NewMapper(ipSetProvider SetProvider, loadBalancerController LoadBalancerProvider, logger *zap.Logger) (*Mapper, error) {
	if ipSetProvider == nil {
		return nil, fmt.Errorf("ipSetProvider must not be nil")
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &Mapper{
		hostPortToMapping:      make(map[hostPort]*portMapping),
		serviceKeyToMappings:   make(map[types.NamespacedName]portMappings),
		ipSetProvider:          ipSetProvider,
		loadBalancerController: loadBalancerController,
		logger:                 logger,
	}, nil
}

// Reconcile applies the desired set of port mappings for a single Service.
//
// It diffs the request against current state, removes mappings that are no longer wanted,
// and adds new ones. Mappings whose host port, service port, and host IP set are
// unchanged are left alone. A host port that is currently owned by a different service
// is a hard error.
//
// Pure-removal calls (empty desired set) do not depend on the IP set provider. That
// matters because Service deletions and annotation removals must succeed even when host
// IP discovery is temporarily broken.
//
// When the host IP set is empty (configured bind CIDRs match nothing right now), the
// mapping is recorded as pending without a load balancer. A later Reconcile that sees
// non-empty IPs will recycle it into a real load balancer.
func (m *Mapper) Reconcile(set MappingSet) error {
	logger := m.logger.With(zap.Stringer("svc-key", set.ServiceKey))
	logger.Debug("reconcile mappings", zap.Int("mapping-count", len(set.Mappings)))

	desired := make(map[hostPort]Mapping, len(set.Mappings))
	for _, mapping := range set.Mappings {
		desired[hostPort(mapping.HostPort)] = mapping
	}

	var hostIPSet ipSet

	if len(desired) > 0 {
		ips, err := m.ipSetProvider.Get()
		if err != nil {
			return fmt.Errorf("failed to get matching IP set: %w", err)
		}

		hostIPSet = ips
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	// any desired host port already owned by a different service is a hard error.
	for port := range desired {
		if existing, ok := m.hostPortToMapping[port]; ok && existing.serviceKey != set.ServiceKey {
			return fmt.Errorf("host port %d is already registered to another service: %s", port, existing.serviceKey)
		}
	}

	var (
		toRemove []hostPort
		toAdd    []Mapping
	)

	// remove anything we previously owned that's no longer in the desired set.
	for port := range m.serviceKeyToMappings[set.ServiceKey] {
		if _, ok := desired[port]; !ok {
			toRemove = append(toRemove, port)
		}
	}

	// classify each desired entry: unchanged (skip), changed (recycle), or new (add).
	for port, mapping := range desired {
		existing, ok := m.hostPortToMapping[port]
		if !ok {
			toAdd = append(toAdd, mapping)

			continue
		}

		if existing.mapping.Equal(mapping) && maps.Equal(existing.hostIPSet, hostIPSet) {
			continue
		}

		toRemove = append(toRemove, port)
		toAdd = append(toAdd, mapping)
	}

	slices.Sort(toRemove)
	slices.SortFunc(toAdd, func(a, b Mapping) int { return cmp.Compare(a.HostPort, b.HostPort) })

	for _, port := range toRemove {
		m.remove(port)
	}

	for _, mapping := range toAdd {
		if err := m.add(set.ServiceKey, mapping, hostIPSet, logger); err != nil {
			return fmt.Errorf("failed to add mapping for host port %d: %w", mapping.HostPort, err)
		}
	}

	return nil
}

// KnownServices returns a sorted snapshot of the service keys this mapper currently
// tracks. Used by the periodic refresh loop to enqueue Reconcile work.
func (m *Mapper) KnownServices() []types.NamespacedName {
	m.lock.Lock()
	defer m.lock.Unlock()

	keys := make([]types.NamespacedName, 0, len(m.serviceKeyToMappings))
	for key := range m.serviceKeyToMappings {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, func(a, b types.NamespacedName) int {
		if c := cmp.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}

		return cmp.Compare(a.Name, b.Name)
	})

	return keys
}

// RefreshIPSet invalidates the underlying IP set cache. The next Reconcile call will see
// freshly fetched host IPs.
func (m *Mapper) RefreshIPSet() error {
	if _, err := m.ipSetProvider.Refresh(); err != nil {
		return fmt.Errorf("failed to refresh IP set: %w", err)
	}

	return nil
}

// Close tears down all active load balancers. Safe to call multiple times.
func (m *Mapper) Close() {
	m.lock.Lock()
	defer m.lock.Unlock()

	for _, port := range slices.Sorted(maps.Keys(m.hostPortToMapping)) {
		m.remove(port)
	}
}

func (m *Mapper) add(serviceKey types.NamespacedName, mapping Mapping, hostIPSet ipSet, logger *zap.Logger) error {
	pm := &portMapping{
		hostIPSet:  hostIPSet,
		serviceKey: serviceKey,
		mapping:    mapping,
	}

	if len(hostIPSet) > 0 {
		lb, err := m.startLoadBalancer(serviceKey, mapping, hostIPSet, logger)
		if err != nil {
			return err
		}

		pm.lb = lb
	} else {
		logger.Info("no host IPs match bind CIDRs, mapping is pending until IPs become available",
			zap.Stringer("mapping", mapping),
		)
	}

	port := hostPort(mapping.HostPort)
	m.hostPortToMapping[port] = pm

	mappings, ok := m.serviceKeyToMappings[serviceKey]
	if !ok {
		mappings = make(portMappings)
		m.serviceKeyToMappings[serviceKey] = mappings
	}

	mappings[port] = pm

	logger.Info("added mapping",
		zap.Stringer("mapping", mapping),
		zap.Strings("ips", slices.Sorted(maps.Keys(hostIPSet))),
	)

	return nil
}

func (m *Mapper) startLoadBalancer(serviceKey types.NamespacedName, mapping Mapping, hostIPSet ipSet, logger *zap.Logger) (LoadBalancer, error) {
	// use an error level logger to avoid spamming the logs with upstream health check failure warnings
	lbLogger := logger.Named("loadbalancer").WithOptions(zap.IncreaseLevel(zap.ErrorLevel))

	lb, err := m.loadBalancerController.New(lbLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to create loadbalancer: %w", err)
	}

	svcName := serviceKey.Name + "." + serviceKey.Namespace

	for ip := range hostIPSet {
		listenAddr := net.JoinHostPort(ip, strconv.Itoa(mapping.HostPort))
		upstreamAddr := net.JoinHostPort(svcName, strconv.Itoa(mapping.ServicePort))

		logger.Debug("add loadbalancer route", zap.String("listen-addr", listenAddr), zap.String("upstream-addr", upstreamAddr))

		if err = lb.AddRoute(listenAddr,
			slices.Values([]string{upstreamAddr}),
			upstream.WithHealthcheckTimeout(time.Second),
		); err != nil {
			return nil, fmt.Errorf("failed to add loadbalancer route (listen=%s upstream=%s): %w", listenAddr, upstreamAddr, err)
		}
	}

	logger.Debug("start loadbalancer")

	if err = lb.Start(); err != nil {
		// we still need to close the loadbalancer, so that the health check goroutines get terminated
		if closeErr := lb.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("failed to start loadbalancer: %w", err), fmt.Errorf("failed to close loadbalancer: %w", closeErr))
		}

		return nil, fmt.Errorf("failed to start loadbalancer: %w", err)
	}

	return lb, nil
}

func (m *Mapper) remove(port hostPort) {
	logger := m.logger.With(zap.Int("host-port", int(port)))

	existing, ok := m.hostPortToMapping[port]
	if !ok {
		logger.Debug("mapping does not exist")

		return
	}

	serviceKey := existing.serviceKey
	logger = logger.With(zap.Stringer("svc-key", serviceKey))

	if ce := logger.Check(zap.DebugLevel, "mapping found, removing"); ce != nil {
		ce.Write(
			zap.Stringer("mapping", existing.mapping),
			zap.Strings("ips", slices.Sorted(maps.Keys(existing.hostIPSet))),
		)
	}

	if existing.lb != nil {
		if err := existing.lb.Close(); err != nil {
			logger.Info("error on closing load balancer", zap.Error(err))
		} else if err = existing.lb.Wait(); err != nil && !errors.Is(err, net.ErrClosed) {
			// net.ErrClosed is the expected signal that the listener was closed
			// via Close above; anything else is worth logging.
			logger.Info("error on waiting for load balancer to close", zap.Error(err))
		}
	}

	delete(m.hostPortToMapping, port)

	mappings, ok := m.serviceKeyToMappings[serviceKey]
	if !ok {
		logger.Warn("per-service entry missing for known host port, this should not happen")

		return
	}

	if _, ok = mappings[port]; !ok {
		logger.Warn("per-service entry missing host port for known mapping, this should not happen")

		return
	}

	delete(mappings, port)

	if len(mappings) == 0 {
		delete(m.serviceKeyToMappings, serviceKey)
	}

	logger.Info("removed mapping")
}
