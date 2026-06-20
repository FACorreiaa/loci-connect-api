package observability

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RequestsTotal tracks total number of unary RPC requests.
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_rpc_requests_total",
			Help: "Total number of unary RPC requests",
		},
		[]string{"procedure", "code"},
	)

	// RequestDuration tracks unary request duration.
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "loci_rpc_duration_seconds",
			Help:    "Unary RPC request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"procedure"},
	)

	// ActiveRequests tracks currently active unary requests.
	ActiveRequests = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "loci_rpc_active_requests",
			Help: "Number of active unary RPC requests",
		},
		[]string{"procedure"},
	)

	// StreamRequestsTotal tracks streaming RPC completions.
	StreamRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_rpc_stream_requests_total",
			Help: "Total number of streaming RPC requests",
		},
		[]string{"procedure", "code"},
	)

	// StreamRequestDuration tracks streaming RPC duration.
	StreamRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "loci_rpc_stream_duration_seconds",
			Help:    "Streaming RPC duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"procedure"},
	)

	// StreamActiveRequests tracks active streaming RPC handlers.
	StreamActiveRequests = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "loci_rpc_stream_active_requests",
			Help: "Number of active streaming RPC requests",
		},
		[]string{"procedure"},
	)
)

// MetricsInterceptor collects Prometheus metrics for unary and streaming RPCs.
type MetricsInterceptor struct{}

var _ connect.Interceptor = (*MetricsInterceptor)(nil)

// NewMetricsInterceptor creates a metrics interceptor.
func NewMetricsInterceptor() *MetricsInterceptor {
	return &MetricsInterceptor{}
}

// WrapUnary implements connect.Interceptor.
func (i *MetricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure

		ActiveRequests.WithLabelValues(procedure).Inc()
		defer ActiveRequests.WithLabelValues(procedure).Dec()

		start := time.Now()
		defer func() {
			RequestDuration.WithLabelValues(procedure).Observe(time.Since(start).Seconds())
		}()

		resp, err := next(ctx, req)
		RequestsTotal.WithLabelValues(procedure, connectCode(err)).Inc()
		return resp, err
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *MetricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *MetricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		procedure := conn.Spec().Procedure

		StreamActiveRequests.WithLabelValues(procedure).Inc()
		defer StreamActiveRequests.WithLabelValues(procedure).Dec()

		start := time.Now()
		defer func() {
			StreamRequestDuration.WithLabelValues(procedure).Observe(time.Since(start).Seconds())
		}()

		err := next(ctx, conn)
		StreamRequestsTotal.WithLabelValues(procedure, connectCode(err)).Inc()
		return err
	}
}

func connectCode(err error) string {
	if err == nil {
		return "ok"
	}
	if connectErr, ok := err.(*connect.Error); ok {
		return connectErr.Code().String()
	}
	return "unknown"
}
