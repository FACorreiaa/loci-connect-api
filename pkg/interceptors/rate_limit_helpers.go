package interceptors

import (
	"context"
	"errors"
	"net"
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

func clientIPFromHeader(header http.Header, peerAddr string) string {
	if header != nil {
		if forwarded := header.Get("X-Forwarded-For"); forwarded != "" {
			if ip := strings.TrimSpace(strings.Split(forwarded, ",")[0]); ip != "" {
				return ip
			}
		}
		if realIP := strings.TrimSpace(header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}
	if peerAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(peerAddr)
	if err != nil {
		return peerAddr
	}
	return host
}

func clientIPFromUnary(ctx context.Context, req connect.AnyRequest) string {
	_ = ctx
	return clientIPFromHeader(req.Header(), req.Peer().Addr)
}

func clientIPFromStream(conn connect.StreamingHandlerConn) string {
	return clientIPFromHeader(conn.RequestHeader(), conn.Peer().Addr)
}

func userIDFromContext(ctx context.Context) string {
	userID, ok := GetUserIDFromContext(ctx)
	if !ok {
		return ""
	}
	return userID
}
