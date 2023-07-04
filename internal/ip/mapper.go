// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"sync"

	"github.com/siderolabs/gen/maps"
	"go.uber.org/zap"
)

// SetProvider in an interface for getting a set of IP addresses.
type SetProvider interface {
	Get() (map[string]struct{}, error)
}

type portMapping struct {
	hostIPSet map[string]struct{}
	lb        LoadBalancer
	svcName   string
	svcPort   int
	hostPort  int
}

// Mapper maps IP addresses on the host to Kubernetes Service resources.
type Mapper struct {
	ipSetProvider          SetProvider
	loadBalancerController LoadBalancerProvider
	hostPortToMapping      map[int]*portMapping
	svcNameToPortMapping   map[string]*portMapping
	logger                 *zap.Logger
	lock                   sync.Mutex
}

// NewMapper returns a new Mapper.
func NewMapper(ipSetProvider SetProvider, loadBalancerController LoadBalancerProvider, logger *zap.Logger) (*Mapper, error) {
	if ipSetProvider == nil {
		return nil, fmt.Errorf("ipSetProvider must not be nil")
	}

	if loadBalancerController == nil {
		loadBalancerController = &TCPLoadBalancerProvider{}
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &Mapper{
		hostPortToMapping:      make(map[int]*portMapping),
		svcNameToPortMapping:   make(map[string]*portMapping),
		ipSetProvider:          ipSetProvider,
		loadBalancerController: loadBalancerController,
		logger:                 logger,
	}, nil
}

// Remove removes the mapping for the given service name.
//
// It will close any existing load balancer for that service and clear any related mappings.
func (m *Mapper) Remove(svcName string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.removeNoLock(svcName)
}

// Add adds a new mapping for the given service name.
//
// It will create a new load balancer and the related mappings.
//
// If there is an existing mapping for another service, an error is returned.
func (m *Mapper) Add(svcName string, hostPort, svcPort int) error {
	logger := m.logger.With(zap.String("svc-name", svcName), zap.Int("host-port", hostPort), zap.Int("svc-port", svcPort))

	logger.Debug("add mapping")

	m.lock.Lock()
	defer m.lock.Unlock()

	hostIPSet, err := m.ipSetProvider.Get()
	if err != nil {
		return fmt.Errorf("failed to get matching IP set: %w", err)
	}

	existingMappingForHostPort := m.hostPortToMapping[hostPort]
	if existingMappingForHostPort != nil && existingMappingForHostPort.svcName != svcName {
		return fmt.Errorf("host port %d is already registered to another service: %s", hostPort, existingMappingForHostPort.svcName)
	}

	existingMappingForService := m.svcNameToPortMapping[svcName]
	if existingMappingForService != nil {
		if existingMappingForService.hostPort == hostPort &&
			existingMappingForService.svcPort == svcPort &&
			reflect.DeepEqual(existingMappingForService.hostIPSet, hostIPSet) {
			m.logger.Info("nothing to do, no changes in mapping")

			return nil
		}

		m.removeNoLock(svcName)
	}

	if len(hostIPSet) == 0 {
		logger.Debug("skip creating new loadbalancer, no matching IPs found")

		return nil
	}

	// use an error level logger to avoid spamming the logs with upstream health check failure warnings
	lbLogger := logger.With(zap.String("component", "loadbalancer")).WithOptions(zap.IncreaseLevel(zap.ErrorLevel))

	lb, err := m.loadBalancerController.New(lbLogger)
	if err != nil {
		return fmt.Errorf("failed to create loadbalancer: %w", err)
	}

	for ip := range hostIPSet {
		if err = lb.AddRoute(net.JoinHostPort(ip, strconv.Itoa(hostPort)),
			[]string{net.JoinHostPort(svcName, strconv.Itoa(svcPort))},
		); err != nil {
			return fmt.Errorf("failed to add route to loadbalancer: %w", err)
		}
	}

	if err = lb.Start(); err != nil {
		return fmt.Errorf("failed to start loadbalancer: %w", err)
	}

	mapping := &portMapping{
		hostIPSet: hostIPSet,
		svcName:   svcName,
		svcPort:   svcPort,
		hostPort:  hostPort,
		lb:        lb,
	}

	m.hostPortToMapping[hostPort] = mapping
	m.svcNameToPortMapping[svcName] = mapping

	logger.Info("added mapping", zap.Strings("ips", maps.Keys(hostIPSet)))

	return nil
}

func (m *Mapper) removeNoLock(svcName string) {
	logger := m.logger.With(zap.String("svc-name", svcName))

	logger.Debug("remove mapping if exists")

	mapping := m.svcNameToPortMapping[svcName]
	if mapping == nil {
		return
	}

	if mapping.lb != nil {
		if err := mapping.lb.Close(); err != nil {
			logger.Info("error on closing load balancer", zap.Error(err))
		}
	}

	delete(m.hostPortToMapping, mapping.hostPort)
	delete(m.svcNameToPortMapping, svcName)

	logger.Info("removed mapping")
}
