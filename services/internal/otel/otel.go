// Package otel wires up tracer/meter providers and the otelhttp middleware
// shared by every Go service.
package otel

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Providers bundles the tracer and meter providers a service should hold
// for the lifetime of the process, plus a Shutdown to flush both on exit.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *metric.MeterProvider

	// shutdownTracer/shutdownMeter default to the real providers' Shutdown
	// methods; tests override them to exercise the error paths in Shutdown
	// without needing a provider that can actually fail to flush.
	shutdownTracer func(context.Context) error
	shutdownMeter  func(context.Context) error
}

// newResource is resource.New by default; tests override it to exercise
// NewProviders' error path.
var newResource = resource.New

// NewProviders builds a tracer provider and a meter provider tagged with
// serviceName, and registers them as the global providers.
func NewProviders(ctx context.Context, serviceName string) (*Providers, error) {
	if serviceName == "" {
		return nil, fmt.Errorf("otel: service name is required")
	}

	res, err := newResource(ctx, resource.WithAttributes(semconv.ServiceName(serviceName)))
	if err != nil {
		return nil, fmt.Errorf("otel: build resource: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithResource(res))
	meterProvider := metric.NewMeterProvider(metric.WithResource(res))

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)

	return &Providers{
		Tracer:         tracerProvider,
		Meter:          meterProvider,
		shutdownTracer: tracerProvider.Shutdown,
		shutdownMeter:  meterProvider.Shutdown,
	}, nil
}

// Shutdown flushes and shuts down both providers, returning the first error
// encountered.
func (p *Providers) Shutdown(ctx context.Context) error {
	if err := p.shutdownTracer(ctx); err != nil {
		return fmt.Errorf("otel: shutdown tracer provider: %w", err)
	}
	if err := p.shutdownMeter(ctx); err != nil {
		return fmt.Errorf("otel: shutdown meter provider: %w", err)
	}
	return nil
}

// Middleware wraps h with otelhttp instrumentation, labeling spans with
// operation.
func Middleware(operation string, h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, operation)
}
