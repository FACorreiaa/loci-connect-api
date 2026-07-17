package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Per-key request rate limit. Coarse abuse guard; the daily LLM quota is the
// real cost control and is enforced separately at generation call sites.
const (
	requestsPerMinute = 60
	requestBurst      = 10
	maxLimiterEntries = 10_000
)

// authMiddleware authenticates requests with a loci_sk_ API key presented as
// a Bearer token, applies a per-key rate limit, and injects the key owner's
// identity plus a quota consumer into the request context. The service layer
// then behaves exactly as it would for a JWT call, and LLM-generation call
// sites meter usage against the owner's daily quota. The Connect interceptor
// chain does not apply here; this is the MCP endpoint's entire auth story.
func authMiddleware(deps Deps, next http.Handler) http.Handler {
	limiters := &limiterStore{entries: make(map[string]*rate.Limiter)}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			unauthorized(w, "missing Authorization: Bearer <loci_sk_...> header")
			return
		}

		key, err := deps.APIKeyService.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, apikey.ErrNotFound) {
				unauthorized(w, "invalid, revoked, or expired API key")
				return
			}
			deps.Logger.ErrorContext(r.Context(), "api key authentication failed", slog.Any("error", err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if !limiters.allow(key.ID.String()) {
			w.Header().Set("Retry-After", "10")
			writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded; slow down and retry")
			return
		}

		userID := key.UserID
		ctx := interceptors.ContextWithClaims(r.Context(), &interceptors.Claims{
			UserID: userID.String(),
		})
		if deps.Subscription != nil {
			ctx = subscription.WithQuotaConsumer(ctx, func(qctx context.Context) error {
				return deps.Subscription.ConsumeQuota(qctx, userID, "")
			})
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(auth[len(prefix):]), true
}

func unauthorized(w http.ResponseWriter, msg string) {
	writeJSONError(w, http.StatusUnauthorized, msg)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// limiterStore is a bounded per-key token-bucket map, mirroring the pattern
// used by pkg/interceptors' IP and user rate limiters.
type limiterStore struct {
	mu      sync.Mutex
	entries map[string]*rate.Limiter
}

func (s *limiterStore) allow(key string) bool {
	s.mu.Lock()
	lim, ok := s.entries[key]
	if !ok {
		if len(s.entries) >= maxLimiterEntries {
			for existing := range s.entries {
				delete(s.entries, existing)
				break
			}
		}
		lim = rate.NewLimiter(rate.Limit(float64(requestsPerMinute)/60.0), requestBurst)
		s.entries[key] = lim
	}
	s.mu.Unlock()
	return lim.Allow()
}
