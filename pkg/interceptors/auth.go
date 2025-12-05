package interceptors

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"
)

// Claims represents the JWT claims for authenticated users
type Claims struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// Context keys for storing user information
type contextKey string

const (
	UserIDKey contextKey = "user_id" // Keep for backward compatibility
	claimsKey contextKey = "claims"
)

// AuthInterceptor handles JWT authentication for Connect RPC
type AuthInterceptor struct {
	jwtSecret          []byte
	optionalProcedures map[string]struct{}
}

var _ connect.Interceptor = (*AuthInterceptor)(nil)

// NewAuthInterceptor creates a new JWT authentication interceptor
func NewAuthInterceptor(jwtSecret []byte, optionalProcedures ...string) *AuthInterceptor {
	procedureMap := make(map[string]struct{}, len(optionalProcedures))
	for _, procedure := range optionalProcedures {
		if procedure != "" {
			procedureMap[procedure] = struct{}{}
		}
	}

	return &AuthInterceptor{
		jwtSecret:          jwtSecret,
		optionalProcedures: procedureMap,
	}
}

// UnaryInterceptor returns a Connect unary interceptor that validates JWT tokens
func (a *AuthInterceptor) UnaryInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			// Skip service for client-side calls
			if req.Spec().IsClient {
				return next(ctx, req)
			}

			// Allow certain procedures to skip strict auth (e.g. Register).
			_, optional := a.optionalProcedures[req.Spec().Procedure]

			// Extract token from Authorization header
			authHeader := req.Header().Get("Authorization")
			if authHeader == "" {
				if optional {
					return next(ctx, req)
				}
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("missing authorization header"),
				)
			}

			// Expected format: "Bearer <token>"
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("invalid authorization header format, expected 'Bearer <token>'"),
				)
			}

			tokenString := parts[1]

			// Parse and validate JWT
			claims := &Claims{}
			token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
				// Validate signing method
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, errors.New("unexpected signing method")
				}
				return a.jwtSecret, nil
			})
			if err != nil {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("invalid token: "+err.Error()),
				)
			}

			if !token.Valid {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("token is not valid"),
				)
			}

			// Check token expiration
			if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("token has expired"),
				)
			}

			// Add claims to context
			ctx = context.WithValue(ctx, claimsKey, claims)
			ctx = context.WithValue(ctx, UserIDKey, claims.UserID) // Backward compatibility

			return next(ctx, req)
		}
	}
}

// OptionalAuthInterceptor returns an interceptor that allows both authenticated and unauthenticated requests
func (a *AuthInterceptor) OptionalAuthInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			if req.Spec().IsClient {
				return next(ctx, req)
			}

			authHeader := req.Header().Get("Authorization")
			if authHeader != "" {
				parts := strings.SplitN(authHeader, " ", 2)
				if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
					claims := &Claims{}
					token, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (interface{}, error) {
						if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
							return nil, errors.New("unexpected signing method")
						}
						return a.jwtSecret, nil
					})

					if err == nil && token.Valid && (claims.ExpiresAt == nil || claims.ExpiresAt.After(time.Now())) {
						ctx = context.WithValue(ctx, claimsKey, claims)
						ctx = context.WithValue(ctx, UserIDKey, claims.UserID)
					}
				}
			}

			return next(ctx, req)
		}
	}
}

// GetClaimsFromContext retrieves the JWT claims from the context
func GetClaimsFromContext(ctx context.Context) (*Claims, error) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	if !ok {
		return nil, errors.New("user not found in context")
	}
	return claims, nil
}

// MustGetClaimsFromContext retrieves claims or panics (use in service with service interceptor)
func MustGetClaimsFromContext(ctx context.Context) *Claims {
	claims, err := GetClaimsFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return claims
}

// GetUserIDFromContext extracts user ID from context (backward compatibility)
func GetUserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(UserIDKey).(string)
	return userID, ok
}

// NewRoleAuthInterceptor checks if the user has the required role
func NewRoleAuthInterceptor(requiredRoles ...string) connect.UnaryInterceptorFunc {
	roleMap := make(map[string]bool)
	for _, role := range requiredRoles {
		roleMap[role] = true
	}

	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			if req.Spec().IsClient {
				return next(ctx, req)
			}

			claims, err := GetClaimsFromContext(ctx)
			if err != nil {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("authentication required"),
				)
			}

			// Admin always has access
			if claims.Role == "admin" {
				return next(ctx, req)
			}

			// Check if user has required role
			if !roleMap[claims.Role] {
				return nil, connect.NewError(
					connect.CodePermissionDenied,
					errors.New("insufficient permissions"),
				)
			}

			return next(ctx, req)
		}
	}
}

// WrapUnary implements connect.Interceptor.
func (a *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return a.UnaryInterceptor()(next)
}

// WrapStreamingClient implements connect.Interceptor (no streaming support yet).
func (a *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor with JWT authentication for streaming.
func (a *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// Skip authentication for client-side calls
		if conn.Spec().IsClient {
			return next(ctx, conn)
		}

		// Allow certain procedures to skip strict auth
		_, optional := a.optionalProcedures[conn.Spec().Procedure]

		// Extract token from Authorization header
		authHeader := conn.RequestHeader().Get("Authorization")
		if authHeader == "" {
			if optional {
				return next(ctx, conn)
			}
			return connect.NewError(
				connect.CodeUnauthenticated,
				errors.New("missing authorization header"),
			)
		}

		// Expected format: "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return connect.NewError(
				connect.CodeUnauthenticated,
				errors.New("invalid authorization header format, expected 'Bearer <token>'"),
			)
		}

		tokenString := parts[1]

		// Parse and validate JWT
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			// Validate signing method
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return a.jwtSecret, nil
		})
		if err != nil {
			return connect.NewError(
				connect.CodeUnauthenticated,
				errors.New("invalid token: "+err.Error()),
			)
		}

		if !token.Valid {
			return connect.NewError(
				connect.CodeUnauthenticated,
				errors.New("token is not valid"),
			)
		}

		// Check token expiration
		if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
			return connect.NewError(
				connect.CodeUnauthenticated,
				errors.New("token has expired"),
			)
		}

		// Add claims to context
		ctx = context.WithValue(ctx, claimsKey, claims)
		ctx = context.WithValue(ctx, UserIDKey, claims.UserID)

		return next(ctx, conn)
	}
}
