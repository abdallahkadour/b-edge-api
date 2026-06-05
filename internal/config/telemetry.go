// telemetry.go initialises OpenTelemetry tracing with a Jaeger OTLP exporter.
package config

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/zap"
)

// NewTelemetry initialises an OpenTelemetry tracer provider that exports
// traces to Jaeger via OTLP/HTTP. Returns a shutdown function that must
// be deferred by the caller.
func NewTelemetry(logger *zap.Logger) (func() error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:4318/v1/traces"
	}

	exporter, err := otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("b-edge-api"),
		semconv.ServiceVersion("1.0.0"),
		semconv.DeploymentEnvironment(os.Getenv("APP_ENV")),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	logger.Info("Telemetry initialised",
		zap.String("exporter", "otlp/http"),
		zap.String("endpoint", endpoint),
	)

	return func() error {
		return tp.Shutdown(context.Background())
	}, nil
}
