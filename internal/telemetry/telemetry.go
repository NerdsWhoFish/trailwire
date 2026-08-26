package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace/noop"
)

type Shutdown func(context.Context) error

var newExporter = func(ctx context.Context) (sdktrace.SpanExporter, error) {
	return otlptracehttp.New(ctx)
}

func Setup(ctx context.Context, version string) (Shutdown, error) {
	enabled, err := enabled()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := newExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OpenTelemetry exporter: %w", err)
	}
	serviceResource, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("trailwire"),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return nil, fmt.Errorf("create OpenTelemetry resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(serviceResource),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return func(shutdownContext context.Context) error {
		err := provider.Shutdown(shutdownContext)
		otel.SetTracerProvider(noop.NewTracerProvider())
		return err
	}, nil
}

func enabled() (bool, error) {
	value := os.Getenv("TRAILWIRE_OTEL_ENABLED")
	if value == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New("TRAILWIRE_OTEL_ENABLED must be true or false")
	}
	return enabled, nil
}
