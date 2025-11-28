package observability

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RequestsTotal tracks total number of RPC requests
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_rpc_requests_total",
			Help: "Total number of RPC requests",
		},
		[]string{"procedure", "code"},
	)

	// RequestDuration tracks request duration
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "loci_rpc_duration_seconds",
			Help:    "RPC request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"procedure"},
	)

	// ActiveRequests tracks currently active requests
	ActiveRequests = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "loci_rpc_active_requests",
			Help: "Number of active RPC requests",
		},
		[]string{"procedure"},
	)
)

// NewMetricsInterceptor creates an interceptor that collects Prometheus metrics
func NewMetricsInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure

			// Track active requests
			ActiveRequests.WithLabelValues(procedure).Inc()
			defer ActiveRequests.WithLabelValues(procedure).Dec()

			// Track duration
			start := time.Now()
			defer func() {
				duration := time.Since(start).Seconds()
				RequestDuration.WithLabelValues(procedure).Observe(duration)
			}()

			// Execute request
			resp, err := next(ctx, req)

			// Track total requests with status code
			code := "ok"
			if err != nil {
				if connectErr, ok := err.(*connect.Error); ok {
					code = connectErr.Code().String()
				} else {
					code = "unknown"
				}
			}
			RequestsTotal.WithLabelValues(procedure, code).Inc()

			return resp, err
		}
	}
}
