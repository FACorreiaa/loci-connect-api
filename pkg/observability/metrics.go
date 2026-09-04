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

	// QuotaConsumedTotal tracks daily LLM request quota consumptions by plan.
	QuotaConsumedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_quota_consumed_total",
			Help: "Daily LLM request quota consumptions by subscription plan",
		},
		[]string{"plan"},
	)

	// QuotaDenialsTotal tracks daily LLM request quota denials by plan.
	QuotaDenialsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_quota_denials_total",
			Help: "Daily LLM request quota denials by subscription plan",
		},
		[]string{"plan"},
	)

	// LLMCallsTotal tracks completed model calls by model and SDK method.
	//
	// One metered chat request fans out into several model calls, so this
	// divided by loci_quota_consumed_total is the fan-out multiplier: the
	// number of model calls a single user action actually costs.
	LLMCallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_llm_calls_total",
			Help: "Completed LLM calls by model and method",
		},
		[]string{"model", "method"},
	)

	// LLMTokensTotal tracks provider-reported token usage by model and kind.
	// Kind is one of prompt, completion, thoughts, cached.
	LLMTokensTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_llm_tokens_total",
			Help: "LLM tokens reported by the provider, by model and token kind",
		},
		[]string{"model", "kind"},
	)

	// CheckoutSessionsCreatedTotal tracks Stripe Checkout sessions created.
	CheckoutSessionsCreatedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "loci_checkout_sessions_created_total",
			Help: "Total Stripe Checkout sessions created",
		},
	)

	// WebhookEventsTotal tracks processed Stripe webhook events by type/outcome.
	WebhookEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_webhook_events_total",
			Help: "Stripe webhook events by type and processing outcome",
		},
		[]string{"type", "status"},
	)

	// WebhookDuplicatesTotal tracks replayed webhook deliveries skipped by
	// the idempotency gate.
	WebhookDuplicatesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "loci_webhook_duplicates_total",
			Help: "Stripe webhook deliveries skipped as already processed",
		},
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
