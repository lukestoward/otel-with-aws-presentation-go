package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	serviceName    = "service-a"
	serviceVersion = "1.3.6"
)

func main() {

	shutdown := initialiseOpenTelemetry()
	defer shutdown()

	r := mux.NewRouter()

	// mux is not an instrumented library (currently), therefore we need to use
	// the instrumentation library to instrument mux for us. This is true for all libraries
	// that are not natively instrumented.
	//
	// github.com/open-telemetry/opentelemetry-go-contrib/blob/main/instrumentation/github.com/gorilla/mux/otelmux/
	r.Use(otelmux.Middleware(serviceName))

	r.HandleFunc("/checkout", checkoutHandler)
	http.Handle("/", r)

	fmt.Println("starting server on port 8000")
	fmt.Printf("-> http://localhost:8000/checkout?basketId=%d\n", rand.Int())

	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}

func checkoutHandler(w http.ResponseWriter, r *http.Request) {
	// Trace information is propagated using the context value.
	// To access the current Span, we use the OTel Trace API to extract this.
	span := trace.SpanFromContext(r.Context())

	// Every Span has an immutable SpanContext which contains the tracing identifiers and options.
	// The SpanContext is serialised and propagated across program boundaries.
	//
	// opentelemetry.io/docs/reference/specification/overview/#spancontext
	traceID := span.SpanContext().TraceID().String()

	// Like the mux router, the standard library HTTP client is not instrumented. Therefore, we
	// need the instrumentation library to instrument the HTTP client for us.
	//
	// github.com/open-telemetry/opentelemetry-go-contrib/tree/main/instrumentation/net/http/otelhttp
	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

	// Extract the basket ID value from the query string.
	query := r.URL.Query()
	basketID := query.Get("basketId")

	// Create a new transaction ID for this order.
	transactionID := uuid.New().String()

	if err := makePayment(r.Context(), client, basketID, transactionID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)

	response := fmt.Sprintf(`{"traceId": "%s", "xray":"%s"}`, traceID, "1-"+traceID[0:8]+"-"+traceID[8:])
	w.Header().Add("ContentType", "application/json")
	_, _ = w.Write(([]byte)(response))
}

func makePayment(ctx context.Context, client http.Client, basketID string, transactionID string) error {
	// To create a new child span we must first retrieve the global TraceProvider. We registered
	// this earlier. Next, we can get a Tracer, using the service name to identify our service.
	// Finally, we can start a new span describing the current operation.
	// An updated Context value is returned containing the updated trace state,
	ctx, span := otel.GetTracerProvider().
		Tracer(serviceName).
		Start(ctx, "Make Payment",
			trace.WithAttributes(
				attribute.String("basket.id", basketID),
				attribute.String("transaction.id", transactionID),
			))

	defer span.End()

	url := fmt.Sprintf("%s/payment?transactionId=%s", os.Getenv("PAYMENT_SERVICE_HOST"), transactionID)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("service-b request error: %v", err)
	}
	defer res.Body.Close()

	return nil
}
