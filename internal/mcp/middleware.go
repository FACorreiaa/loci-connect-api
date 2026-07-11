package mcp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/apikey"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// authMiddleware authenticates requests with a loci_sk_ API key presented as
// a Bearer token and injects the key owner's identity into the request
// context, so the service layer sees the same identity a JWT call would.
// The Connect interceptor chain does not apply here; this is the MCP
// endpoint's entire auth story.
func authMiddleware(deps Deps, next http.Handler) http.Handler {
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

		ctx := interceptors.ContextWithClaims(r.Context(), &interceptors.Claims{
			UserID: key.UserID.String(),
		})
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
