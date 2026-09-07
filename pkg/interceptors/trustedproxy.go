package interceptors

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// trustedNet is one address or range whose forwarding headers may be believed.
type trustedNet struct {
	prefix netip.Prefix
}

func (t *trustedNet) contains(addr netip.Addr) bool {
	return t != nil && t.prefix.Contains(addr)
}

// ParseTrustedProxies reads the TRUSTED_PROXIES setting: a list of CIDRs, or
// bare addresses meaning that host alone.
//
// Blank entries are dropped so a trailing comma in configuration is not an
// error. Anything else unparseable IS an error rather than a silent drop:
// quietly trusting nothing looks identical to a working configuration until
// somebody notices every caller sharing one rate-limit bucket.
func ParseTrustedProxies(entries []string) ([]*trustedNet, error) {
	var nets []*trustedNet

	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}

		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q is not a valid CIDR", entry)
			}
			nets = append(nets, &trustedNet{prefix: prefix.Masked()})
			continue
		}

		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is not a valid address or CIDR", entry)
		}
		nets = append(nets, &trustedNet{prefix: netip.PrefixFrom(addr, addr.BitLen())})
	}

	return nets, nil
}

// trusted reports whether an address belongs to a configured proxy.
func trusted(addr netip.Addr, nets []*trustedNet) bool {
	for _, n := range nets {
		if n.contains(addr) {
			return true
		}
	}
	return false
}

// parseAddr reads an address that may or may not carry a port, and may be a
// bracketed IPv6 host.
func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}

	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Unmap(), true
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap(), true
		}
	}
	return netip.Addr{}, false
}
