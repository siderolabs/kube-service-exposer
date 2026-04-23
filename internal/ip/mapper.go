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

	"github.com/siderolabs/gen/maps"
	"github.com/siderolabs/go-loadbalancer/upstream"
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
		logger.Info("failed to get matching IP set", zap.Error(err))

		return fmt.Errorf("failed to get matching IP set: %w", err)
	}

	logger.Debug("resolved host IP set", zap.Int("ip-count", len(hostIPSet)), zap.Strings("ips", maps.Keys(hostIPSet)))

	existingMappingForHostPort := m.hostPortToMapping[hostPort]
	if existingMappingForHostPort != nil && existingMappingForHostPort.svcName != svcName {
		logger.Warn("host port conflict: already registered to another service",
			zap.String("conflicting-svc", existingMappingForHostPort.svcName),
		)

		return fmt.Errorf("host port %d is already registered to another service: %s", hostPort, existingMappingForHostPort.svcName)
	}

	existingMappingForService := m.svcNameToPortMapping[svcName]
	if existingMappingForService != nil {
		logger.Debug("existing mapping found",
			zap.Int("existing-host-port", existingMappingForService.hostPort),
			zap.Int("existing-svc-port", existingMappingForService.svcPort),
			zap.Int("existing-ip-count", len(existingMappingForService.hostIPSet)),
			zap.Strings("existing-ips", maps.Keys(existingMappingForService.hostIPSet)),
		)

		if existingMappingForService.hostPort == hostPort &&
			existingMappingForService.svcPort == svcPort &&
			reflect.DeepEqual(existingMappingForService.hostIPSet, hostIPSet) {
			logger.Debug("nothing to do, no changes in mapping")

			return nil
		}

		logger.Debug("existing mapping changed, replacing it")

		m.removeNoLock(svcName)
	}

	if len(hostIPSet) == 0 {
		logger.Debug("skip creating new loadbalancer, no matching IPs found")

		return nil
	}

	// use an error level logger to avoid spamming the logs with upstream health check failure warnings
	lbLogger := logger.Named("loadbalancer").WithOptions(zap.IncreaseLevel(zap.ErrorLevel))

	lb, err := m.loadBalancerController.New(lbLogger)
	if err != nil {
		logger.Info("failed to create loadbalancer", zap.Error(err))

		return fmt.Errorf("failed to create loadbalancer: %w", err)
	}

	for ip := range hostIPSet {
		listenAddr := net.JoinHostPort(ip, strconv.Itoa(hostPort))
		upstreamAddr := net.JoinHostPort(svcName, strconv.Itoa(svcPort))

		logger.Debug("add loadbalancer route", zap.String("listen-addr", listenAddr), zap.String("upstream-addr", upstreamAddr))

		if err = lb.AddRoute(listenAddr,
			slices.Values([]string{upstreamAddr}),
			upstream.WithHealthcheckTimeout(time.Second),
		); err != nil {
			logger.Info("failed to add loadbalancer route", zap.String("listen-addr", listenAddr), zap.String("upstream-addr", upstreamAddr), zap.Error(err))

			return fmt.Errorf("failed to add route to loadbalancer: %w", err)
		}
	}

	logger.Debug("start loadbalancer")

	if err = lb.Start(); err != nil {
		logger.Info("failed to start loadbalancer, attempt to stop it")

		// we still need to close the loadbalancer, so that the health checks goroutines get terminated
		if closeErr := lb.Close(); closeErr != nil {
			return errors.Join(fmt.Errorf("failed to start loadbalancer: %w", err), fmt.Errorf("failed to close loadbalancer: %w", closeErr))
		}

		return fmt.Errorf("failed to start loadbalancer: %w", err)
	}

	logger.Debug("loadbalancer started")

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
		logger.Debug("mapping does not exist")

		return
	}

	logger.Debug("mapping found, removing",
		zap.Int("host-port", mapping.hostPort),
		zap.Int("svc-port", mapping.svcPort),
		zap.Int("ip-count", len(mapping.hostIPSet)),
		zap.Strings("ips", maps.Keys(mapping.hostIPSet)),
	)

	if mapping.lb != nil {
		logger.Debug("close loadbalancer")

		if err := mapping.lb.Close(); err != nil {
			logger.Info("error on closing load balancer", zap.Error(err))
		} else {
			logger.Debug("wait for loadbalancer to stop")

			// successfully closed, wait for the proxy to finish running
			if err = mapping.lb.Wait(); err != nil {
				logger.Info("error on waiting for load balancer to close", zap.Error(err))
			} else {
				logger.Debug("loadbalancer stopped")
			}
		}
	}

	delete(m.hostPortToMapping, mapping.hostPort)
	delete(m.svcNameToPortMapping, svcName)

	logger.Info("removed mapping")
}
