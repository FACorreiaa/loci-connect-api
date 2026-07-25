package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

// InitTracing installs a real TracerProvider exporting OTLP/HTTP when
// OTEL_EXPORTER_OTLP_ENDPOINT is set. Without it, the global no-op provider
// stays in place (spans cost ~nothing) and the returned shutdown is a no-op —
// tracing is opt-in per environment.
func InitTracing(ctx context.Context, serviceName string, logger *slog.Logger) (func(context.Context) error, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		logger.Info("OTEL_EXPORTER_OTLP_ENDPOINT not set; tracing disabled")
		return func(context.Context) error { return nil }, nil
	}

	// A set-but-malformed endpoint (e.g. "https://", scheme only) used to sail
	// past the emptiness check and leave the exporter posting to a hostless URL,
	// logging `traces export: Post "https:///": http: no Host in request URL`
	// every batch interval, forever. Validate here and disable tracing instead
	// of shipping a broken exporter.
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		logger.Warn("OTEL_EXPORTER_OTLP_ENDPOINT is not a valid http(s) URL with a host; tracing disabled",
			"endpoint", endpoint)
		return func(context.Context) error { return nil }, nil
	}

	// Pass the endpoint explicitly rather than relying on the exporter re-reading
	// the environment, so what we validated is what gets used.
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	// Use a schemaless resource for our extra attributes so merging with
	// resource.Default() never fails on a schema-URL mismatch when the OTel SDK
	// (and its built-in semconv) is bumped ahead of this package's semconv pin.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to build OTel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	logger.Info("OTel tracing enabled", "endpoint", endpoint, "service", serviceName)
	return tp.Shutdown, nil
}
