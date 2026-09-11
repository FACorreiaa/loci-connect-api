# Build stage
FROM golang:1.27.0-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make ca-certificates wget

WORKDIR /app

# Set GOTOOLCHAIN to auto to allow Go to download the required version
ENV GOTOOLCHAIN=auto

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies (Go will auto-download the required toolchain)
RUN go mod download

# Copy source code (includes embedded migrations in pkg/db/migrations/)
COPY . .

# Build the application with optimizations
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o preference-rerank ./cmd/preference-rerank

# Runtime stage
FROM alpine:latest

# Install runtime dependencies.
#
# opus-tools carries opusenc, which is how a spoken reply becomes something
# Telegram will play as a voice note: the speech provider answers with raw PCM
# and Telegram accepts only Opus in an Ogg container. A subprocess rather than
# a library because Go has no Opus encoder — every binding is cgo, and this
# binary is built CGO_ENABLED=0. It brings five dependencies and a couple of
# megabytes, and also carries opusdec, which is the transcode to reach for if
# the provider ever refuses Telegram's Ogg on the way in.
RUN apk --no-cache add ca-certificates tzdata wget opus-tools \
    && addgroup -S loci && adduser -S -G loci -H loci

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /app/server .
COPY --from=builder /app/preference-rerank .

# Expose ports (API:8080, Metrics:9090, pprof:6060)
EXPOSE 8080 9090 6060

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://localhost:8080/health || exit 1

# Drop root: the server binds unprivileged ports and writes nothing to disk.
USER loci

# Run the application
CMD ["./server"]
