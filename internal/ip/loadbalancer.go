// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip

import (
	"github.com/siderolabs/go-loadbalancer/loadbalancer"
	"github.com/siderolabs/go-loadbalancer/upstream"
	"go.uber.org/zap"
)

// LoadBalancer is an interface for loadbalancer instances.
type LoadBalancer interface {
	AddRoute(ipPort string, upstreamAddrs []string, options ...upstream.ListOption) error
	Start() error
	Close() error
	Wait() error
}

// LoadBalancerProvider is a factory for LoadBalancer instances.
type LoadBalancerProvider interface {
	New(logger *zap.Logger) (LoadBalancer, error)
}

// TCPLoadBalancerProvider is a LoadBalancerProvider that creates and returns loadbalancer.TCP instances.
type TCPLoadBalancerProvider struct{}

// New returns a new loadbalancer.TCP instance.
// New returns a new loadbalancer.TCP instance.
func (t *TCPLoadBalancerProvider) New(logger *zap.Logger) (LoadBalancer, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &loadbalancer.TCP{Logger: logger}, nil
}
