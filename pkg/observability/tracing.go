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

	posthogotel "github.com/posthog/posthog-go/otel"
)

// defaultPostHogHost is PostHog's EU cloud, matching pkg/analytics's default
// for the same POSTHOG_API_KEY/POSTHOG_HOST pair.
const defaultPostHogHost = "https://eu.i.posthog.com"

// InitTracing installs a real TracerProvider when OTEL_EXPORTER_OTLP_ENDPOINT
// and/or POSTHOG_API_KEY are set — the former exports every span as OTLP/HTTP,
// the latter registers PostHog's AI Observability span processor, which
// forwards only the gen_ai.*/llm.*/ai.*/traceloop.* spans LLM calls emit.
// With neither set, the global no-op provider stays in place (spans cost
// ~nothing) and the returned shutdown is a no-op — tracing is opt-in per
// environment.
func InitTracing(ctx context.Context, serviceName string, logger *slog.Logger) (func(context.Context) error, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	posthogKey := strings.TrimSpace(os.Getenv("POSTHOG_API_KEY"))

	// A set-but-malformed endpoint (e.g. "https://", scheme only) used to sail
	// past the emptiness check and leave the exporter posting to a hostless URL,
	// logging `traces export: Post "https:///": http: no Host in request URL`
	// every batch interval, forever. Validate here and disable OTLP export
	// instead of shipping a broken exporter.
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			logger.Warn("OTEL_EXPORTER_OTLP_ENDPOINT is not a valid http(s) URL with a host; OTLP export disabled",
				"endpoint", endpoint)
			endpoint = ""
		}
	}

	if endpoint == "" && posthogKey == "" {
		logger.Info("OTEL_EXPORTER_OTLP_ENDPOINT and POSTHOG_API_KEY not set; tracing disabled")
		return func(context.Context) error { return nil }, nil
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

	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}

	if endpoint != "" {
		// Pass the endpoint explicitly rather than relying on the exporter
		// re-reading the environment, so what we validated is what gets used.
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)))
		logger.Info("OTel tracing enabled", "endpoint", endpoint, "service", serviceName)
	}

	if posthogKey != "" {
		host := strings.TrimSpace(os.Getenv("POSTHOG_HOST"))
		if host == "" {
			host = defaultPostHogHost
		}
		processor, err := posthogotel.NewSpanProcessor(ctx, posthogKey, posthogotel.WithHost(host))
		if err != nil {
			return nil, fmt.Errorf("failed to create PostHog AI Observability span processor: %w", err)
		}
		opts = append(opts, sdktrace.WithSpanProcessor(processor), sdktrace.WithSpanProcessor(AIContextSpanProcessor{}))
		logger.Info("PostHog AI Observability enabled", "host", host)
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
