// Package telemetry menyediakan setup OpenTelemetry tracing -- kirim span
// via OTLP gRPC ke otel-collector (infra/otel/otel-collector-config.yml),
// yang forward ke Jaeger.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitTracer membuat TracerProvider yang mengirim span ke otelEndpoint
// (mis. "localhost:4317") dan mendaftarkannya sebagai global tracer.
// Caller wajib memanggil shutdown yang dikembalikan saat aplikasi berhenti.
func InitTracer(ctx context.Context, serviceName, otelEndpoint string) (shutdown func(context.Context) error, err error) {
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otelEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTEL resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
