// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/siderolabs/gen/maps"
	"github.com/siderolabs/go-loadbalancer/upstream"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Mapping contains information about a mapping between a Kubernetes Service and a host port.
type Mapping struct {
	ServiceKey client.ObjectKey
	HostPort   int
	SvcPort    int
}

// SetProvider in an interface for getting a set of IP addresses.
type SetProvider interface {
	Get() (map[string]struct{}, error)
}

type portMapping struct {
	hostIPSet map[string]struct{}
	lb        LoadBalancer
	svcKey    client.ObjectKey
	svcPort   int
	hostPort  int
}

// Mapper maps IP addresses on the host to Kubernetes Service resources.
type Mapper struct {
	ipSetProvider          SetProvider
	loadBalancerController LoadBalancerProvider
	hostPortToMapping      map[int]*portMapping
	svcKeyToPortMapping    map[client.ObjectKey]*portMapping
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
		hostPortToMapping:      make(map[int]*portMapping),
		svcKeyToPortMapping:    make(map[client.ObjectKey]*portMapping),
		ipSetProvider:          ipSetProvider,
		loadBalancerController: loadBalancerController,
		logger:                 logger,
	}, nil
}

// Remove removes the mapping for the given service key.
//
// It will close any existing load balancer for that service and clear any related mappings.
func (m *Mapper) Remove(svcKey client.ObjectKey) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.removeNoLock(svcKey)
}

// Add adds a new mapping for the given service key.
//
// It will create a new load balancer and the related mappings.
//
// If there is an existing mapping for another service, an error is returned.
func (m *Mapper) Add(mapping Mapping) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.addNoLock(mapping)
}

// Sync syncs the passed mappings.
//
// The passed list of mappings are considered as the full state:
// any existing mapping that is not in the list will be removed,
// and any mapping in the list that does not exist will be added.
func (m *Mapper) Sync(mappings []Mapping) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	desiredKeys := make(map[client.ObjectKey]struct{}, len(mappings))

	for _, mapping := range mappings {
		desiredKeys[mapping.ServiceKey] = struct{}{}
	}

	// process removals first
	for key := range m.svcKeyToPortMapping {
		if _, exists := desiredKeys[key]; !exists {
			m.removeNoLock(key)
		}
	}

	var errs error

	for _, mapping := range mappings {
		if err := m.addNoLock(mapping); err != nil {
			errs = multierror.Append(errs, err)
		}
	}

	return errs
}

func (m *Mapper) removeNoLock(svcKey client.ObjectKey) {
	logger := m.logger.With(zap.Stringer("svc-key", svcKey))

	logger.Debug("remove mapping if exists")

	mapping := m.svcKeyToPortMapping[svcKey]
	if mapping == nil {
		return
	}

	if mapping.lb != nil {
		if err := mapping.lb.Close(); err != nil {
			logger.Info("error on closing load balancer", zap.Error(err))
		} else {
			// successfully closed, wait for the proxy to finish running
			if err = mapping.lb.Wait(); err != nil {
				logger.Info("error on waiting for load balancer to close", zap.Error(err))
			}
		}
	}

	delete(m.hostPortToMapping, mapping.hostPort)
	delete(m.svcKeyToPortMapping, svcKey)

	logger.Info("removed mapping")
}

func (m *Mapper) addNoLock(mapping Mapping) error {
	svcKey := mapping.ServiceKey
	hostPort := mapping.HostPort
	svcPort := mapping.SvcPort

	logger := m.logger.With(zap.Stringer("svc-key", svcKey), zap.Int("host-port", hostPort), zap.Int("svc-port", svcPort))

	logger.Debug("add mapping")

	hostIPSet, err := m.ipSetProvider.Get()
	if err != nil {
		return fmt.Errorf("failed to get matching IP set: %w", err)
	}

	existingMappingForHostPort := m.hostPortToMapping[hostPort]
	if existingMappingForHostPort != nil && existingMappingForHostPort.svcKey != svcKey {
		return fmt.Errorf("host port %d is already registered to another service: %s", hostPort, existingMappingForHostPort.svcKey.String())
	}

	existingMappingForService := m.svcKeyToPortMapping[svcKey]
	if existingMappingForService != nil {
		if existingMappingForService.hostPort == hostPort &&
			existingMappingForService.svcPort == svcPort &&
			reflect.DeepEqual(existingMappingForService.hostIPSet, hostIPSet) {
			logger.Info("nothing to do, no changes in mapping")

			return nil
		}

		m.removeNoLock(svcKey)
	}

	if len(hostIPSet) == 0 {
		logger.Debug("skip creating new loadbalancer, no matching IPs found")

		return nil
	}

	// use an error level logger to avoid spamming the logs with upstream health check failure warnings
	lbLogger := logger.Named("loadbalancer").WithOptions(zap.IncreaseLevel(zap.ErrorLevel))

	lb, err := m.loadBalancerController.New(lbLogger)
	if err != nil {
		return fmt.Errorf("failed to create loadbalancer: %w", err)
	}

	svcHost := svcKey.Name + "." + svcKey.Namespace

	for ip := range hostIPSet {
		if err = lb.AddRoute(net.JoinHostPort(ip, strconv.Itoa(hostPort)),
			slices.Values([]string{net.JoinHostPort(svcHost, strconv.Itoa(svcPort))}),
			upstream.WithHealthcheckTimeout(time.Second),
		); err != nil {
			return fmt.Errorf("failed to add route to loadbalancer: %w", err)
		}
	}

	if err = lb.Start(); err != nil {
		logger.Info("failed to start loadbalancer, attempt to stop it")

		// we still need to close the loadbalancer, so that the health checks goroutines get terminated
		if closeErr := lb.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("failed to start loadbalancer: %w", err), fmt.Errorf("failed to close loadbalancer: %w", closeErr))
		}

		return fmt.Errorf("failed to start loadbalancer: %w", err)
	}

	pMapping := &portMapping{
		hostIPSet: hostIPSet,
		svcKey:    svcKey,
		svcPort:   svcPort,
		hostPort:  hostPort,
		lb:        lb,
	}

	m.hostPortToMapping[hostPort] = pMapping
	m.svcKeyToPortMapping[svcKey] = pMapping

	logger.Info("added mapping", zap.Strings("ips", maps.Keys(hostIPSet)))

	return nil
}
