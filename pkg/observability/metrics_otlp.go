package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"github.com/prometheus/client_golang/prometheus"
)

// metricExportInterval is how often collected metrics are pushed. PostHog
// aggregates on its side, so a slower cadence than the OTel default costs
// nothing in fidelity and keeps the request count down.
const metricExportInterval = 60 * time.Second

// InitMetrics pushes this service's metrics over OTLP/HTTP when
// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT is set. Without it the global no-op meter
// provider stays in place and the returned shutdown does nothing, so metric
// export is opt-in per environment exactly like tracing.
//
// It bridges the existing Prometheus registry rather than re-declaring every
// instrument against the OTel API. Everything already counted — RPC rates, quota
// consumption and denials, LLM calls and tokens, MCP tool calls, trip re-opens —
// is exported as-is, and a new Prometheus metric is picked up with no change
// here. The /metrics endpoint keeps working for anything that scrapes it.
//
// Authentication rides on OTEL_EXPORTER_OTLP_METRICS_HEADERS, read by the
// exporter itself, so no credential is named in this file or passed through
// config. For PostHog that is `Authorization=Bearer <project key>`.
func InitMetrics(ctx context.Context, serviceName string, logger *slog.Logger) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }

	// OTEL_SDK_DISABLED is the spec's global off switch. The Go SDK does not
	// enforce it for providers built by hand, so honour it here: a deployment
	// that sets it means it, and shipping metrics anyway would be a surprise.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		logger.Info("OTEL_SDK_DISABLED is true; metric export disabled")
		return noop, nil
	}

	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"))
	if endpoint == "" {
		logger.Info("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT not set; metric export disabled")
		return noop, nil
	}

	// Same trap the trace exporter fell into: a set-but-malformed endpoint
	// sails past an emptiness check and then logs an export failure on every
	// interval, forever. Refuse it here instead.
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		logger.Warn("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT is not a valid http(s) URL with a host; metric export disabled",
			"endpoint", endpoint)
		return noop, nil
	}

	// WithEndpointURL uses the URL as given. PostHog's path is /i/v1/metrics,
	// not the OTLP default /v1/metrics, so the path must survive intact.
	exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	// Schemaless for the same reason as tracing: merging with resource.Default()
	// must not fail when the SDK's built-in semconv moves ahead of our pin.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to build OTel resource: %w", err)
	}

	// One reader, not two. It collects both this provider's own OTel
	// instruments and, through the producer, the Prometheus registry promauto
	// writes to. A second reader sharing the exporter would simply send
	// everything twice.
	producer := prombridge.NewMetricProducer(prombridge.WithGatherer(prometheus.DefaultGatherer))
	reader := sdkmetric.NewPeriodicReader(
		exporter,
		sdkmetric.WithInterval(metricExportInterval),
		sdkmetric.WithProducer(producer),
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)

	otel.SetMeterProvider(provider)
	logger.Info("OTel metric export enabled",
		"endpoint", endpoint, "service", serviceName, "interval", metricExportInterval)
	return provider.Shutdown, nil
}
