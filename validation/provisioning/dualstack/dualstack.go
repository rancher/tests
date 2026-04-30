package dualstack

import (
	"net/netip"
	"strings"

	"github.com/sirupsen/logrus"
)

// SetCIDROrder reorders a comma-separated CIDR list by IP family.
// If ipv6First is true, IPv6 CIDRs are returned before IPv4 CIDRs.
func SetCIDROrder(cidrList string, ipv6First bool) string {
	cidrs := strings.Split(cidrList, ",")
	ipv4CIDRs := make([]string, 0, len(cidrs))
	ipv6CIDRs := make([]string, 0, len(cidrs))

	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}

		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			logrus.Warnf("Skipping invalid CIDR '%s': %v", cidr, err)
			continue
		}

		if prefix.Addr().Is6() {
			ipv6CIDRs = append(ipv6CIDRs, cidr)
			continue
		}

		if prefix.Addr().Is4() {
			ipv4CIDRs = append(ipv4CIDRs, cidr)
			continue
		}

	}

	ordered := make([]string, 0, len(cidrs))
	if ipv6First {
		ordered = append(ordered, ipv6CIDRs...)
		ordered = append(ordered, ipv4CIDRs...)
	} else {
		ordered = append(ordered, ipv4CIDRs...)
		ordered = append(ordered, ipv6CIDRs...)
	}

	reorderedCIDR := strings.Join(ordered, ",")

	return reorderedCIDR
}
