package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"google.golang.org/grpc"
)

func initialiseOpenTelemetry() func() {

	// A resource describes the entity that is generating the telemetry data.
	// In our case, it describes the specific service instance.
	// All Telemetry data will be associated with the resource that generated it.
	res := createResource()

	// An exporter is responsible for emitting the telemetry data somewhere. This could
	// be to the console, OTel Collector or straight to an external third-party backend.
	exporter := createOLTPExporter()

	// A sampler determines whether or a span will be sampled. You can separately
	// configure the sampling rules for root spans and child spans. Each time a new span
	// is created, the sampler is invoked.
	sampler := createSampler()

	// A Trace Provider connects the instrumented code generating telemetry
	// with the exporter by implementing the OpenTelemetry API.
	traceProvider := createTraceProvider(res, exporter, sampler)

	// Register our TraceProvider instance from the SDK with the OTEL API
	// so that libraries and other instrumented code can retrieve a TraceProvider.
	otel.SetTracerProvider(traceProvider)

	// The propagator is responsible for serialising the Trace information across
	// program boundaries. For example injecting/extracting trace info into/from a HTTP header.
	// Here we're registering the AWS X-Ray propagator as their format is not W3C compliant.
	otel.SetTextMapPropagator(xray.Propagator{})

	// Return a func to gracefully shutdown the TraceProvider and flush any telemetry data.
	shutdown := func() {
		if err := traceProvider.Shutdown(context.Background()); err != nil {
			fmt.Printf("error shutting down trace provider: %v", err)
		}
	}

	return shutdown
}

func createResource() *resource.Resource {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(serviceVersion),
		),
	)

	if err != nil {
		log.Fatalf("error creating otel resource: %v", err)
	}

	return res
}

func createOLTPExporter() sdktrace.SpanExporter {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	otelAgentAddr, ok := os.LookupEnv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if !ok {
		otelAgentAddr = "0.0.0.0:4317"
	}

	exporter, err := otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(otelAgentAddr),
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)

	if err != nil {
		log.Fatalf("failed to create new otlp trace exporter: %v", err)
	}

	return exporter
}

func createSampler() sdktrace.Sampler {
	return sdktrace.ParentBased(sdktrace.AlwaysSample())
}

func createTraceProvider(res *resource.Resource, exporter sdktrace.SpanExporter, sampler sdktrace.Sampler) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
		sdktrace.WithIDGenerator(xray.NewIDGenerator()),
	)
}
