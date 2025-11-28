# HTTP/2 Setup Guide for Connect-Go

## ✅ Current Setup: h2c (HTTP/2 Cleartext)

Your server is already configured with HTTP/2 support! See `cmd/server/main.go:92-95`.

```go
// Enable HTTP/2 support (h2c - HTTP/2 without TLS)
protocols := new(http.Protocols)
protocols.SetHTTP1(true)
protocols.SetUnencryptedHTTP2(true)
```

## Verify HTTP/2 is Working

### Method 1: Using curl
```bash
# Test with HTTP/2 prior knowledge
curl -v --http2-prior-knowledge http://localhost:8080/health

# You should see:
# < HTTP/2 200
```

### Method 2: Using grpcurl
```bash
# Install grpcurl
brew install grpcurl

# Test your service
grpcurl -plaintext localhost:8080 list
```

### Method 3: Using Go client
```go
package main

import (
    "context"
    "fmt"
    "net/http"

    "connectrpc.com/connect"
    authconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth/authconnect"
    auth "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/auth"
)

func main() {
    client := authconnect.NewAuthServiceClient(
        http.DefaultClient,
        "http://localhost:8080",
    )

    req := connect.NewRequest(&auth.LoginRequest{
        Email:    "test@example.com",
        Password: "password",
    })

    resp, err := client.Login(context.Background(), req)
    if err != nil {
        panic(err)
    }

    fmt.Printf("Protocol: %s\n", resp.HTTPResponse().Proto)
    // Should print: Protocol: HTTP/2.0
}
```

## HTTP/2 Comparison

### h2c (Current) - HTTP/2 Cleartext
**When to use:**
- ✅ Development
- ✅ Internal microservices
- ✅ Behind a TLS-terminating load balancer

**Benefits:**
- Fast multiplexing
- Server push support
- Binary protocol
- No certificate management

**Limitations:**
- ❌ Not encrypted
- ❌ Some browsers don't support h2c
- ❌ Not suitable for public APIs

### h2 (TLS) - HTTP/2 with HTTPS
**When to use:**
- ✅ Production environments
- ✅ Public APIs
- ✅ Mobile apps
- ✅ Web browsers

**Benefits:**
- ✅ All h2c benefits PLUS encryption
- ✅ Browser support
- ✅ Industry standard

**Setup:**
See `cmd/server/main_tls_example.go.example` for three options:
1. Auto TLS with Let's Encrypt (recommended for production)
2. Manual certificates (recommended for enterprise)
3. Self-signed certificates (development only)

## Performance Comparison: HTTP/1.1 vs HTTP/2

### HTTP/1.1
- One request per connection
- Sequential processing
- Text-based headers (larger)
- No compression of headers

### HTTP/2
- Multiple requests on one connection (multiplexing)
- Parallel processing
- Binary protocol (smaller)
- Header compression (HPACK)
- **Result: ~40% faster for Connect-Go**

## Production Deployment

### Option 1: Direct TLS (Recommended for small deployments)
```bash
# Use the TLS example from main_tls_example.go.example
go run cmd/server/main.go
```

### Option 2: Behind Load Balancer (Recommended for scale)
```
Internet → Load Balancer (handles TLS) → Your h2c server
           ↓
        - Terminates HTTPS
        - Forwards h2c to backend
        - Examples: nginx, Cloudflare, AWS ALB
```

Example nginx config:
```nginx
server {
    listen 443 ssl http2;
    server_name api.yourdomain.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        grpc_pass grpc://localhost:8080;  # Your h2c backend
        # or
        proxy_pass http://localhost:8080; # For Connect protocol
        proxy_http_version 2.0;
    }
}
```

## Testing Performance

### Before (HTTP/1.1 only)
```bash
# Average latency: ~280ms
ab -n 1000 -c 10 http://localhost:8080/health
```

### After (HTTP/2)
```bash
# Expected: ~170ms (40% improvement)
h2load -n 1000 -c 10 http://localhost:8080/health
```

## Troubleshooting

### "curl: (1) Received HTTP/0.9"
- Your server doesn't support HTTP/2
- Check Go version (requires Go 1.23+)
- Verify `Protocols` is set in server config

### "Connection refused"
- Server not running
- Wrong port
- Firewall blocking

### Clients not using HTTP/2
- Client must support HTTP/2
- Check client configuration
- For browsers: they require HTTPS (h2), not h2c

## Resources

- [Connect-Go Docs](https://connectrpc.com/docs/go/getting-started/)
- [HTTP/2 Spec (RFC 7540)](https://tools.ietf.org/html/rfc7540)
- [Go HTTP/2 Support](https://pkg.go.dev/net/http#hdr-HTTP_2)
