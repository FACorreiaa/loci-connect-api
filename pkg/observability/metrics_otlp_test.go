package observability

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// The metrics exporter is opt-in and must refuse a malformed endpoint rather
// than install an exporter that posts to a hostless URL forever. This is the
// same failure the tracing exporter already learned; the two read different
// environment variables, so the guard has to exist twice.
func TestInitMetrics_EndpointHandling(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A local collector stands in for PostHog. The valid case must exercise the
	// real exporter path, and a test that posts to the internet is a test that
	// fails on a train.
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	cases := []struct {
		name     string
		endpoint string
		wantReal bool
	}{
		{name: "unset", endpoint: "", wantReal: false},
		{name: "whitespace", endpoint: "   ", wantReal: false},
		{name: "scheme only", endpoint: "https://", wantReal: false},
		{name: "no scheme", endpoint: "eu.i.posthog.com/i/v1/metrics", wantReal: false},
		// PostHog's path is /i/v1/metrics, not the OTLP default, so the path has
		// to survive into the exporter untouched.
		{name: "valid endpoint with a non-default path", endpoint: collector.URL + "/i/v1/metrics", wantReal: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			otel.SetMeterProvider(metricnoop.NewMeterProvider())
			t.Setenv("OTEL_SDK_DISABLED", "")
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", tc.endpoint)

			shutdown, err := InitMetrics(context.Background(), "test-service", logger)
			if err != nil {
				t.Fatalf("InitMetrics: %v", err)
			}
			t.Cleanup(func() {
				if err := shutdown(context.Background()); err != nil {
					t.Errorf("shutdown: %v", err)
				}
			})

			_, isNoop := otel.GetMeterProvider().(metricnoop.MeterProvider)
			if tc.wantReal && isNoop {
				t.Fatalf("endpoint %q should have installed a real meter provider", tc.endpoint)
			}
			if !tc.wantReal && !isNoop {
				t.Fatalf("endpoint %q should have left the no-op meter provider in place", tc.endpoint)
			}
		})
	}
}

// Shutdown is always safe to call, including when metrics were never enabled.
func TestInitMetrics_ShutdownIsAlwaysSafe(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")

	shutdown, err := InitMetrics(context.Background(), "test-service", logger)
	if err != nil {
		t.Fatalf("InitMetrics: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown with metrics disabled: %v", err)
	}
}

// OTEL_SDK_DISABLED is the spec's global off switch, and a deployment that sets
// it must not have metrics shipped at it anyway just because an endpoint is
// also configured.
func TestInitMetrics_RespectsTheGlobalDisableFlag(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	otel.SetMeterProvider(metricnoop.NewMeterProvider())
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", collector.URL+"/i/v1/metrics")

	shutdown, err := InitMetrics(context.Background(), "test-service", logger)
	if err != nil {
		t.Fatalf("InitMetrics: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	if _, isNoop := otel.GetMeterProvider().(metricnoop.MeterProvider); !isNoop {
		t.Fatal("OTEL_SDK_DISABLED=true must leave the no-op meter provider in place")
	}
}
