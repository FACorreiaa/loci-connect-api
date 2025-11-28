package interceptors

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracingInterceptor instruments RPCs with OpenTelemetry spans.
type TracingInterceptor struct {
	tracer trace.Tracer
}

// NewTracingInterceptor creates a tracing interceptor.
func NewTracingInterceptor(tracer trace.Tracer) *TracingInterceptor {
	if tracer == nil {
		tracer = otel.Tracer("loci/interceptors")
	}
	return &TracingInterceptor{tracer: tracer}
}

// WrapUnary implements connect.Interceptor.
func (i *TracingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, span := i.tracer.Start(ctx, req.Spec().Procedure, trace.WithSpanKind(trace.SpanKindServer))
		serviceName := serviceFromProcedure(req.Spec().Procedure)
		span.SetAttributes(
			attribute.String("rpc.system", "connect"),
			attribute.String("rpc.service", serviceName),
			attribute.String("rpc.method", req.Spec().Procedure),
		)

		resp, err := next(ctx, req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "ok")
		}
		span.End()
		return resp, err
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *TracingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *TracingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, span := i.tracer.Start(ctx, conn.Spec().Procedure, trace.WithSpanKind(trace.SpanKindServer))
		serviceName := serviceFromProcedure(conn.Spec().Procedure)
		span.SetAttributes(
			attribute.String("rpc.system", "connect"),
			attribute.String("rpc.service", serviceName),
			attribute.String("rpc.method", conn.Spec().Procedure),
		)
		err := next(ctx, conn)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "ok")
		}
		span.End()
		return err
	}
}

func serviceFromProcedure(procedure string) string {
	if procedure == "" {
		return ""
	}
	lastSlash := strings.LastIndex(procedure, "/")
	if lastSlash <= 0 {
		return procedure
	}
	return procedure[:lastSlash]
}
