package telemetry

import (
	"context"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type countingExporter struct {
	spans atomic.Int64
}

func (e *countingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.spans.Add(int64(len(spans)))
	return nil
}

func (e *countingExporter) Shutdown(context.Context) error {
	return nil
}

func TestDisabledByDefaultDoesNotConstructExporter(t *testing.T) {
	t.Setenv("TRAILWIRE_OTEL_ENABLED", "")
	original := newExporter
	t.Cleanup(func() { newExporter = original })
	calls := 0
	newExporter = func(context.Context) (sdktrace.SpanExporter, error) {
		calls++
		return nil, nil
	}
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("exporter constructed %d times while disabled", calls)
	}
}

func TestExplicitOptInExportsSpans(t *testing.T) {
	t.Setenv("TRAILWIRE_OTEL_ENABLED", "true")
	original := newExporter
	t.Cleanup(func() { newExporter = original })
	exporter := &countingExporter{}
	newExporter = func(context.Context) (sdktrace.SpanExporter, error) {
		return exporter, nil
	}
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("trailwire.test").Start(context.Background(), "test span")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exporter.spans.Load() != 1 {
		t.Fatalf("exported %d spans, want 1", exporter.spans.Load())
	}
}
