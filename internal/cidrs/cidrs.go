// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package cidrs provides functions for working with CIDRs.
package cidrs

import (
	"net/netip"
)

// FilterIPSet filters an IP set by a list of CIDRs.
// It returns the filtered IP set.
// If there is an error while parsing an IP address, it will be passed to the errHandler.
func FilterIPSet(cidrs []netip.Prefix, ipSet map[string]struct{}, errHandler func(ip string, err error)) map[string]struct{} {
	filteredIPSet := make(map[string]struct{}, len(cidrs)*2)

	for ip := range ipSet {
		parsedIP, err := netip.ParseAddr(ip)
		if err != nil {
			if errHandler != nil {
				errHandler(ip, err)
			}

			continue
		}

		for _, cidr := range cidrs {
			if cidr.Contains(parsedIP) {
				filteredIPSet[parsedIP.String()] = struct{}{}
			}
		}
	}

	return filteredIPSet
}
