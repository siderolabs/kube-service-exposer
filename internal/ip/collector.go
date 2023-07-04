// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ip

import (
	"fmt"
	"net"
)

// Collector collects IP addresses on all network interfaces.
type Collector struct{}

// NewCollector returns a new Collector.
func NewCollector() *Collector {
	return &Collector{}
}

// Get returns a set of IP addresses on all network interfaces.
func (c *Collector) Get() (map[string]struct{}, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get interfaces: %w", err)
	}

	ips := make(map[string]struct{}, len(ifaces)*2)

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("failed to get addresses for interface %q: %w", iface.Name, err)
		}

		for _, addr := range addrs {
			var ip net.IP

			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			ips[ip.String()] = struct{}{}
		}
	}

	return ips, nil
}
