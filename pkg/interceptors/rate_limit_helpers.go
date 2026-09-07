package interceptors

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"
)

func allowWithRetry(limiter *rate.Limiter) (bool, time.Duration) {
	if limiter == nil {
		return true, 0
	}
	res := limiter.Reserve()
	if !res.OK() {
		return false, 0
	}
	delay := res.Delay()
	if delay > 0 {
		res.Cancel()
		return false, delay
	}
	return true, 0
}

func newRateLimitError(retryAfter time.Duration) *connect.Error {
	err := connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded"))
	if retryAfter > 0 {
		secs := int(retryAfter.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		err.Meta().Set("Retry-After", strconv.Itoa(secs))
	}
	return err
}

type keyedLimiterStore struct {
	mu         sync.Mutex
	limiters   map[string]*rate.Limiter
	maxEntries int
	rate       rate.Limit
	burst      int
}

func newKeyedLimiterStore(perSecond, burst, maxEntries int) *keyedLimiterStore {
	if perSecond <= 0 || burst <= 0 || maxEntries <= 0 {
		return nil
	}
	return &keyedLimiterStore{
		limiters:   make(map[string]*rate.Limiter),
		maxEntries: maxEntries,
		rate:       rate.Limit(float64(perSecond)),
		burst:      burst,
	}
}

func (s *keyedLimiterStore) limiterFor(key string) *rate.Limiter {
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if lim, ok := s.limiters[key]; ok {
		return lim
	}
	if len(s.limiters) >= s.maxEntries {
		for existing := range s.limiters {
			delete(s.limiters, existing)
			break
		}
	}
	lim := rate.NewLimiter(s.rate, s.burst)
	s.limiters[key] = lim
	return lim
}

// clientIPFromHeader decides which address a request is rate limited against.
//
// Forwarding headers are believed only when the immediate peer is a configured
// proxy. Without that check the headers are attacker-controlled: any caller
// could pick its own bucket and defeat the limit entirely, or name somebody
// else's address and spend their budget. The peer address is the one thing a
// caller cannot forge, so it is what an untrusted connection is keyed on.
//
// When the peer IS trusted, X-Forwarded-For is a chain — "client, proxy1,
// proxy2" — and it is walked from the right. Entries a trusted proxy appended
// are trustworthy; the first entry to the left of the trusted run is the
// furthest back we can believe, because everything beyond it was supplied by
// whoever called that proxy.
//
// trustedProxies empty means trust nothing, which is correct and safe when the
// server is reached directly. Behind an ingress it makes every caller share one
// bucket, so deployments that sit behind one must configure TRUSTED_PROXIES —
// cmd/api/router.go warns at boot when they have not.
func clientIPFromHeader(header http.Header, peerAddr string, trustedProxies []*trustedNet) string {
	peer, peerOK := parseAddr(peerAddr)

	peerFallback := func() string {
		if peerOK {
			return peer.String()
		}
		// Not an address we can parse. Returned verbatim rather than dropped,
		// so an unusual transport still gets a stable key.
		return strings.TrimSpace(peerAddr)
	}

	if header == nil || !peerOK || !trusted(peer, trustedProxies) {
		return peerFallback()
	}

	if forwarded := header.Get("X-Forwarded-For"); forwarded != "" {
		hops := strings.Split(forwarded, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			addr, ok := parseAddr(hops[i])
			if !ok {
				// Unparseable entries are skipped rather than used: distinct
				// junk values would each open their own bucket, which is an
				// unbounded-cardinality hole in the store as well as nonsense.
				continue
			}
			if !trusted(addr, trustedProxies) {
				return addr.String()
			}
		}
		// Every hop was a trusted proxy, so the leftmost is as far back as the
		// chain goes.
		for _, hop := range hops {
			if addr, ok := parseAddr(hop); ok {
				return addr.String()
			}
		}
	}

	if addr, ok := parseAddr(header.Get("X-Real-IP")); ok {
		return addr.String()
	}
	return peerFallback()
}

func clientIPFromUnary(ctx context.Context, req connect.AnyRequest, trustedProxies []*trustedNet) string {
	_ = ctx
	return clientIPFromHeader(req.Header(), req.Peer().Addr, trustedProxies)
}

func clientIPFromStream(conn connect.StreamingHandlerConn, trustedProxies []*trustedNet) string {
	return clientIPFromHeader(conn.RequestHeader(), conn.Peer().Addr, trustedProxies)
}

func userIDFromContext(ctx context.Context) string {
	userID, ok := GetUserIDFromContext(ctx)
	if !ok {
		return ""
	}
	return userID
}
