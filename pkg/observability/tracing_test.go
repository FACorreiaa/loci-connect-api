package observability

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// A malformed OTEL_EXPORTER_OTLP_ENDPOINT must disable tracing rather than
// install an exporter that posts to a hostless URL and logs
// `http: no Host in request URL` on every batch flush.
func TestInitTracing_EndpointHandling(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cases := []struct {
		name     string
		endpoint string
		wantReal bool
	}{
		{name: "unset", endpoint: "", wantReal: false},
		{name: "scheme only", endpoint: "https://", wantReal: false},
		{name: "no scheme", endpoint: "collector:4318", wantReal: false},
		{name: "whitespace", endpoint: "   ", wantReal: false},
		{name: "valid", endpoint: "https://otlp.example.com:4318", wantReal: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset the global provider between cases so each one genuinely
			// reports whether *this* call installed a real provider.
			otel.SetTracerProvider(noop.NewTracerProvider())
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.endpoint)

			shutdown, err := InitTracing(context.Background(), "test-service", logger)
			if err != nil {
				t.Fatalf("InitTracing returned error: %v", err)
			}
			t.Cleanup(func() { _ = shutdown(context.Background()) })

			_, isReal := otel.GetTracerProvider().(*sdktrace.TracerProvider)
			if isReal != tc.wantReal {
				t.Fatalf("endpoint %q: real TracerProvider installed = %v, want %v",
					tc.endpoint, isReal, tc.wantReal)
			}
		})
	}
}
