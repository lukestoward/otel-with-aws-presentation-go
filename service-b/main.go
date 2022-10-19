package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	serviceName    = "service-b"
	serviceVersion = "2.0.4"
)

func main() {

	shutdown := initialiseOpenTelemetry()
	defer shutdown()

	// Create AWS components
	cfg := getAWSConfig()
	sqsClient := newSQSClient(cfg)

	sqsQueueURL := os.Getenv("SQS_QUEUE_URL")

	rand.Seed(time.Now().UnixNano())

	r := mux.NewRouter()
	r.Use(otelmux.Middleware(serviceName))

	r.HandleFunc("/payment", paymentHandler(sqsClient, sqsQueueURL))
	http.Handle("/", r)

	fmt.Println("starting server on port 8001")

	if err := http.ListenAndServe(":8001", nil); err != nil {
		log.Fatal(err)
	}
}

func paymentHandler(sqsClient *sqs.Client, queueURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// Extract the basket ID value from the query string.
		query := r.URL.Query()
		transactionID := query.Get("transactionId")

		receiptID := takePayment(r.Context(), transactionID)

		// Once payment has been processed, send a record of the transaction to the SQS queue.
		messageBody := fmt.Sprintf(`{"transactionId": "%s", "receiptId": "%s"}`, transactionID, receiptID)

		input := sqs.SendMessageInput{
			MessageBody: &messageBody,
			QueueUrl:    &queueURL,
		}

		_, err := sqsClient.SendMessage(r.Context(), &input)
		if err != nil {
			fmt.Printf("error sending sqs message: %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

func takePayment(ctx context.Context, transactionID string) string {
	ctx, span := otel.GetTracerProvider().
		Tracer(serviceName).
		Start(ctx, "Process Payment", trace.WithAttributes(attribute.String("transaction.id", transactionID)))

	defer span.End()

	// Simulate random latency to process payment
	minSleep := 1
	maxSleep := 5
	sleep := rand.Intn(maxSleep-minSleep+1) + minSleep
	time.Sleep(time.Duration(sleep * int(time.Second)))

	// Generate a random payment receipt ID
	receiptID := uuid.New().String()

	// Add the receiptID to the current span attributes
	span.SetAttributes(attribute.String("payment.receipt.id", receiptID))

	return receiptID
}
