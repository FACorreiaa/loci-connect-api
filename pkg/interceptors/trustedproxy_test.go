package interceptors

import (
	"net/http"
	"strings"
	"testing"
)

func mustParse(t *testing.T, cidrs ...string) []*trustedNet {
	t.Helper()
	nets, err := ParseTrustedProxies(cidrs)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%v): %v", cidrs, err)
	}
	return nets
}

// The whole point of the change. Before it, any caller could set this header
// and be rate limited as an address of their choosing — or spend somebody
// else's budget.
func TestAnUntrustedPeerCannotChooseItsOwnRateLimitKey(t *testing.T) {
	header := http.Header{}
	header.Set("X-Forwarded-For", "198.51.100.7")
	header.Set("X-Real-IP", "203.0.113.1")

	// No proxy configured: the connection is the only thing worth believing.
	got := clientIPFromHeader(header, "45.33.32.156:41234", nil)
	if got != "45.33.32.156" {
		t.Fatalf("client IP = %q, want the peer address; a spoofed header was believed", got)
	}
}

// Behind an ingress the peer is always the proxy, so without trusting it every
// caller shares one bucket and the first few to arrive spend the budget for
// everybody.
func TestATrustedPeersForwardedHeaderIsBelieved(t *testing.T) {
	trusted := mustParse(t, "10.42.0.0/16")
	header := http.Header{"X-Forwarded-For": []string{"198.51.100.7"}}

	got := clientIPFromHeader(header, "10.42.3.9:55123", trusted)
	if got != "198.51.100.7" {
		t.Fatalf("client IP = %q, want 198.51.100.7", got)
	}
}

// The header is a chain: "client, proxy1, proxy2". Everything a trusted proxy
// appended is trustworthy; everything to the left of that was supplied by
// whoever called it, and the first such entry is the furthest we can believe.
func TestTheChainIsWalkedFromTheRight(t *testing.T) {
	trusted := mustParse(t, "10.42.0.0/16", "172.16.0.0/12")

	cases := map[string]struct {
		forwarded string
		want      string
	}{
		"one hop":              {"198.51.100.7", "198.51.100.7"},
		"client then proxy":    {"198.51.100.7, 10.42.0.9", "198.51.100.7"},
		"two trusted hops":     {"198.51.100.7, 172.16.4.4, 10.42.0.9", "198.51.100.7"},
		"spoofed prefix":       {"10.42.9.9, 198.51.100.7, 10.42.0.9", "198.51.100.7"},
		"all trusted":          {"10.42.1.1, 10.42.0.9", "10.42.1.1"},
		"untrusted after real": {"198.51.100.7, 203.0.113.9, 10.42.0.9", "203.0.113.9"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			header := http.Header{"X-Forwarded-For": []string{tc.forwarded}}
			if got := clientIPFromHeader(header, "10.42.3.9:1", trusted); got != tc.want {
				t.Errorf("forwarded %q -> %q, want %q", tc.forwarded, got, tc.want)
			}
		})
	}
}

// A trusted proxy that sets X-Real-IP instead of X-Forwarded-For.
func TestRealIPIsBelievedOnlyFromATrustedPeer(t *testing.T) {
	trusted := mustParse(t, "10.42.0.0/16")

	// Set, not a map literal: Go canonicalises this key to "X-Real-Ip", so a
	// literal spelled "X-Real-IP" is never found by Get. Real requests are
	// canonicalised when they are parsed, so only hand-built headers hit this.
	header := http.Header{}
	header.Set("X-Real-IP", "198.51.100.7")

	if got := clientIPFromHeader(header, "10.42.3.9:1", trusted); got != "198.51.100.7" {
		t.Errorf("trusted peer: got %q, want 198.51.100.7", got)
	}
	if got := clientIPFromHeader(header, "45.33.32.156:1", trusted); got != "45.33.32.156" {
		t.Errorf("untrusted peer: got %q, want the peer address", got)
	}
}

// Garbage in the header must never become a rate-limit key: distinct junk
// values would each open their own bucket, which is an unbounded-cardinality
// hole in the store as well as nonsense.
//
// What it falls back to is deliberately not asserted. With every hop either
// junk or a trusted proxy there is no client address in the chain, and either
// the innermost trusted hop or the peer is a defensible key — both are inside
// the trusted network. The invariant worth pinning is that the result is a real
// address and not the attacker's string.
func TestUnparseableForwardedEntriesNeverBecomeKeys(t *testing.T) {
	trusted := mustParse(t, "10.42.0.0/16")

	for name, forwarded := range map[string]string{
		"not an ip":  "not-an-ip, 10.42.0.9",
		"empty":      " , 10.42.0.9",
		"hostname":   "evil.example.com, 10.42.0.9",
		"sql-ish":    "'; DROP TABLE, 10.42.0.9",
		"whitespace": "   ",
		"all junk":   "aaa, bbb, ccc",
	} {
		t.Run(name, func(t *testing.T) {
			got := clientIPFromHeader(http.Header{"X-Forwarded-For": []string{forwarded}}, "10.42.3.9:1", trusted)

			if _, ok := parseAddr(got); !ok {
				t.Fatalf("key %q is not an address", got)
			}
			for _, junk := range []string{"not-an-ip", "evil.example.com", "DROP TABLE", "aaa"} {
				if strings.Contains(got, junk) {
					t.Fatalf("key %q carries the header's junk", got)
				}
			}
		})
	}
}

// An entry that carries a port is a real address, not junk — some proxies
// append one — so it is used, with the port dropped.
func TestAForwardedEntryWithAPortIsStillAnAddress(t *testing.T) {
	trusted := mustParse(t, "10.42.0.0/16")
	header := http.Header{"X-Forwarded-For": []string{"198.51.100.7:9999, 10.42.0.9"}}

	if got := clientIPFromHeader(header, "10.42.3.9:1", trusted); got != "198.51.100.7" {
		t.Errorf("got %q, want 198.51.100.7", got)
	}
}

func TestNoHeadersFallsBackToThePeer(t *testing.T) {
	if got := clientIPFromHeader(nil, "45.33.32.156:41234", nil); got != "45.33.32.156" {
		t.Errorf("got %q, want 45.33.32.156", got)
	}
	// An address with no port is still an address.
	if got := clientIPFromHeader(nil, "45.33.32.156", nil); got != "45.33.32.156" {
		t.Errorf("got %q, want 45.33.32.156", got)
	}
	if got := clientIPFromHeader(nil, "", nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestIPv6PeersAndProxies(t *testing.T) {
	trusted := mustParse(t, "2a01:4f8::/32")
	header := http.Header{"X-Forwarded-For": []string{"2001:db8::1, 2a01:4f8:c015::9"}}

	if got := clientIPFromHeader(header, "[2a01:4f8:c015::9]:443", trusted); got != "2001:db8::1" {
		t.Errorf("got %q, want 2001:db8::1", got)
	}
	// An untrusted v6 peer cannot pick its own key either.
	if got := clientIPFromHeader(header, "[2001:db8::99]:443", trusted); got != "2001:db8::99" {
		t.Errorf("got %q, want the peer address", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	t.Run("accepts CIDRs and bare addresses", func(t *testing.T) {
		nets := mustParse(t, "10.42.0.0/16", "192.168.1.5", " 172.16.0.0/12 ", "", "2a01:4f8::/32")
		if len(nets) != 4 {
			t.Fatalf("parsed %d entries, want 4 (blanks dropped)", len(nets))
		}
	})

	t.Run("a bare address trusts only itself", func(t *testing.T) {
		trusted := mustParse(t, "192.168.1.5")
		header := http.Header{"X-Forwarded-For": []string{"198.51.100.7"}}

		if got := clientIPFromHeader(header, "192.168.1.5:1", trusted); got != "198.51.100.7" {
			t.Errorf("the named proxy was not trusted: %q", got)
		}
		if got := clientIPFromHeader(header, "192.168.1.6:1", trusted); got != "192.168.1.6" {
			t.Errorf("a neighbour was trusted: %q", got)
		}
	})

	t.Run("rejects nonsense rather than silently trusting nothing", func(t *testing.T) {
		for _, raw := range []string{"not-a-cidr", "10.42.0.0/99", "10.42.0.0/-1"} {
			if _, err := ParseTrustedProxies([]string{raw}); err == nil {
				t.Errorf("ParseTrustedProxies(%q) accepted it", raw)
			}
		}
	})
}
