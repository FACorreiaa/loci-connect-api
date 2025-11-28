# Server Package

This package contains the main application entry point with clean separation of concerns.

## File Overview

### main.go (92 lines)
**Purpose**: Application lifecycle management

**Responsibilities**:
- Initialize logger
- Load configuration
- Call dependency injection
- Setup router
- Start HTTP server
- Handle graceful shutdown

**What it does NOT do**:
- ❌ Create repositories
- ❌ Create services
- ❌ Create handlers
- ❌ Register routes
- ❌ Setup interceptors

**Key Functions**:
```go
func main()
func runServer(cfg *config.Config, logger *slog.Logger, handler http.Handler) error
```

---

### dependencies.go (101 lines)
**Purpose**: Dependency injection container

**Responsibilities**:
- Hold all application dependencies
- Initialize database connection
- Create repositories (with DB dependency)
- Create services (with repository dependencies)
- Create handlers (with service dependencies)
- Cleanup resources

**Key Type**:
```go
type Dependencies struct {
    Config *config.Config
    DB     *db.DB
    Logger *slog.Logger

    // Repositories
    MyServiceRepo repository.MyServiceRepository

    // Services
    MyServiceSvc service.MyServiceService

    // Handlers
    MyServiceHandler *handlers.MyServiceHandler
}
```

**Key Functions**:
```go
func InitDependencies(cfg *config.Config, logger *slog.Logger) (*Dependencies, error)
func (d *Dependencies) initDatabase() error
func (d *Dependencies) initRepositories()
func (d *Dependencies) initServices()
func (d *Dependencies) initHandlers()
func (d *Dependencies) Cleanup()
```

**Initialization Order**:
1. Database → 2. Repositories → 3. Services → 4. Handlers

---

### router.go (79 lines)
**Purpose**: Route registration and middleware setup

**Responsibilities**:
- Create HTTP mux
- Setup interceptor chain
- Register Connect RPC routes
- Register utility routes (health, metrics)
- Return configured HTTP handler

**Key Functions**:
```go
func SetupRouter(deps *Dependencies) http.Handler
func registerConnectRoutes(mux *http.ServeMux, deps *Dependencies, opts connect.HandlerOption)
func registerUtilityRoutes(mux *http.ServeMux, deps *Dependencies)
```

**Interceptor Chain**:
1. Recovery (panic handling)
2. Logging (request/response logging)
3. Auth (JWT validation)
4. Metrics (Prometheus)

**Registered Routes**:
- `/proto.myservice.v1.MyService/*` - Connect RPC service
- `/health` - Database health check
- `/ready` - Readiness probe
- `/metrics` - Prometheus metrics

---

## Usage

### Running the Server

```bash
# Development
make dev

# Production
make build
./bin/server
```

### Adding a New Service

1. **Update dependencies.go**:
```go
type Dependencies struct {
    // ... existing ...

    // Add new fields
    UserRepo repository.UserRepository
    UserSvc service.UserService
    UserHandler *handlers.UserHandler
}

func (d *Dependencies) initRepositories() {
    d.MyServiceRepo = repository.NewMyServiceRepository(d.DB.DB)
    d.UserRepo = repository.NewUserRepository(d.DB.DB)  // Add this
}

func (d *Dependencies) initServices() {
    d.MyServiceSvc = service.NewMyServiceService(d.MyServiceRepo)
    d.UserSvc = service.NewUserService(d.UserRepo)  // Add this
}

func (d *Dependencies) initHandlers() {
    d.MyServiceHandler = handlers.NewMyServiceHandler(d.MyServiceSvc, d.Logger)
    d.UserHandler = handlers.NewUserHandler(d.UserSvc, d.Logger)  // Add this
}
```

2. **Update router.go**:
```go
func registerConnectRoutes(mux *http.ServeMux, deps *Dependencies, opts connect.HandlerOption) {
    // Existing MyService...

    // Add new service
    userServicePath, userServiceHandler := userserviceconnect.NewUserServiceHandler(
        deps.UserHandler,
        opts,
    )
    mux.Handle(userServicePath, userServiceHandler)
    deps.Logger.Info("registered Connect RPC service", "path", userServicePath)
}
```

3. **No changes to main.go required!**

---

## Design Benefits

### 1. Single Responsibility Principle
Each file has one clear purpose:
- `main.go` → App lifecycle
- `dependencies.go` → Dependency management
- `router.go` → Route configuration

### 2. Dependency Injection
All dependencies are explicitly wired, making the code:
- Testable (easy to mock dependencies)
- Maintainable (clear dependency graph)
- Type-safe (compiler checks all dependencies)

### 3. Clean main.go
The main function is only 47 lines and reads like a script:
```go
1. Initialize logger
2. Load config
3. Initialize dependencies
4. Setup router
5. Run server
```

### 4. Easy Testing
Each component can be tested independently:

```go
// Test service with mock service
func TestHandler(t *testing.T) {
    mockService := &MockService{}
    handler := handlers.NewMyServiceHandler(mockService, logger)
    // Test...
}

// Test service with mock repo
func TestService(t *testing.T) {
    mockRepo := &MockRepository{}
    service := service.NewMyServiceService(mockRepo)
    // Test...
}
```

### 5. Scalability
Adding new services requires only:
1. Add fields to `Dependencies` struct
2. Add initialization in `init*` methods
3. Register routes in `router.go`

No refactoring of existing code needed!

---

## Code Metrics

| File | Lines | Purpose | Complexity |
|------|-------|---------|------------|
| main.go | 92 | App lifecycle | Low |
| dependencies.go | 101 | Dependency injection | Medium |
| router.go | 79 | Route registration | Low |

**Total**: 272 lines for complete server setup with DI and routing.

---

## Architecture Diagram

```
main.go
  │
  ├─→ InitDependencies() ──→ dependencies.go
  │                            │
  │                            ├─→ initDatabase()
  │                            ├─→ initRepositories()
  │                            ├─→ initServices()
  │                            └─→ initHandlers()
  │
  ├─→ SetupRouter() ──────→ router.go
  │                            │
  │                            ├─→ Interceptor chain
  │                            ├─→ Register Connect routes
  │                            └─→ Register utility routes
  │
  └─→ runServer() ─────────→ HTTP Server with graceful shutdown
```

---

## Environment Variables

See [.env.example](../../.env.example) for all configuration options.

Required variables:
- `DATABASE_URL` - PostgreSQL connection string
- `SERVER_PORT` - Server port (default: 8080)

Optional variables:
- `METRICS_ENABLED` - Enable Prometheus metrics (default: true)
- `METRICS_PORT` - Metrics port (default: 9090)

---

## Monitoring

### Health Checks
- `GET /health` - Returns 200 if database is healthy
- `GET /ready` - Returns 200 if server is ready

### Metrics
- `GET /metrics` - Prometheus metrics endpoint

### Structured Logging
All logs use structured JSON format:
```json
{
  "time": "2024-01-01T12:00:00Z",
  "level": "INFO",
  "msg": "server started",
  "addr": "localhost:8080"
}
```

---

For more details, see [ARCHITECTURE.md](../../ARCHITECTURE.md).
