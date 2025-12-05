# Streaming Authentication Fix

## 🐛 **Problem**

When searching for itineraries (or any content using `StreamChat` RPC), users were getting authentication errors even though they were logged in:

```json
{"error":{"code":"unauthenticated","message":"authentication required"}}
```

### Why Restaurants/Hotels Worked But Itineraries Didn't

- **Restaurants/Hotels**: In some flows, these were using unary RPC endpoints that had proper authentication
- **Itineraries**: Always use `StreamChat` (server streaming RPC), which had **NO authentication support**

## 🔍 **Root Cause**

The authentication interceptor in `/Users/fernando_idwell/Projects/Loci/loci-connect-server/pkg/interceptors/auth.go` had this:

```go
// BEFORE (BROKEN):
func (a *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next  // ❌ No authentication! Just passes through
}
```

The `WrapStreamingHandler` function:
- Did NOT validate JWT tokens
- Did NOT extract user ID from token
- Did NOT add user info to context
- Comment literally said: "no streaming support yet"

When `StreamChat` handler tried to get user ID from context:

```go
userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
if !ok || userIDStr == "" {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
}
```

It failed because the context was empty (no auth interceptor ran).

---

## ✅ **Solution**

Implemented full JWT authentication in `WrapStreamingHandler` to match `WrapUnary`:

```go
// AFTER (FIXED):
func (a *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// 1. Skip client-side calls
		if conn.Spec().IsClient {
			return next(ctx, conn)
		}

		// 2. Allow optional procedures to skip auth
		_, optional := a.optionalProcedures[conn.Spec().Procedure]

		// 3. Extract Authorization header
		authHeader := conn.RequestHeader().Get("Authorization")
		if authHeader == "" {
			if optional {
				return next(ctx, conn)
			}
			return connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
		}

		// 4. Parse "Bearer <token>" format
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid authorization header format"))
		}

		// 5. Validate JWT token
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return a.jwtSecret, nil
		})

		if err != nil || !token.Valid {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
		}

		// 6. Check expiration
		if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("token has expired"))
		}

		// 7. Add user info to context
		ctx = context.WithValue(ctx, claimsKey, claims)
		ctx = context.WithValue(ctx, UserIDKey, claims.UserID)

		return next(ctx, conn)
	}
}
```

---

## 🧪 **Testing**

### 1. Start the Server

```bash
cd /Users/fernando_idwell/Projects/Loci/loci-connect-server
make run
```

### 2. Start the Client

```bash
cd /Users/fernando_idwell/Projects/Loci/go-ai-poi-client
npm run dev
```

### 3. Test Itinerary Search

1. **Log in** to the application
2. Go to Dashboard
3. Search for: **"Itinerary in Lisbon"**
4. **Expected**:
   - ✅ Request succeeds (no authentication error)
   - ✅ Page navigates to `/itinerary?...`
   - ✅ Itinerary data streams progressively
   - ✅ No "authentication required" error

### 4. Check Browser Console

You should see:
```
🚀 Using REAL server streaming (not fake!)
🔔 Received SSE event: {Type: "start", ...}
🔔 Received SSE event: {Type: "itinerary", ...}
🔔 Received SSE event: {Type: "complete", ...}
✅ Streaming complete
```

**NOT**:
```
❌ {"error":{"code":"unauthenticated","message":"authentication required"}}
```

### 5. Check Server Logs

Server should log successful authentication:
```
{"level":"INFO","msg":"authenticated user","user_id":"...","procedure":"/loci.chat.ChatService/StreamChat"}
```

---

## 📝 **Files Modified**

1. **`/Users/fernando_idwell/Projects/Loci/loci-connect-server/pkg/interceptors/auth.go`**
   - Implemented `WrapStreamingHandler` with full JWT authentication
   - Added token validation, expiration checking, and context population
   - Now matches the behavior of `WrapUnary` for streaming RPCs

---

## 🎯 **Impact**

### Before:
- ❌ Itinerary searches fail with auth error
- ❌ All streaming RPC calls bypass authentication
- ❌ Security vulnerability (anyone could call streaming endpoints)

### After:
- ✅ Itinerary searches work properly
- ✅ All streaming RPCs validate JWT tokens
- ✅ User ID correctly extracted and added to context
- ✅ Security hole closed

---

## 🚀 **What's Next**

Now that authentication is fixed, you can proceed with:

1. **Apply progressive loading to hotels/activities pages**
2. **Add progressive map marker loading**
3. **Add staggered card animations**
4. **Add haptic feedback on mobile**

All of these enhancements will now work properly since streaming authentication is functioning!

---

## 🔒 **Security Note**

This fix closes a **critical security vulnerability** where streaming RPC endpoints were completely unauthenticated. Any client could call these endpoints without a valid JWT token. Now all streaming endpoints properly validate authentication before processing requests.
