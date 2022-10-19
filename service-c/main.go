package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamoTypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	serviceName    = "service-c"
	serviceVersion = "1.0.1"
)

func main() {

	shutdown := initialiseOpenTelemetry()
	defer shutdown()

	// Create AWS components
	cfg := getAWSConfig()
	sqsClient := newSQSClient(cfg)
	s3Client := newS3Client(cfg)
	dynamoClient := newDynamoClient(cfg)

	queueURL := os.Getenv("SQS_QUEUE_URL")
	bucket := os.Getenv("S3_BUCKET_NAME")
	table := os.Getenv("DYNAMO_TABLE_NAME")

	httpClient := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

	rand.Seed(time.Now().UnixNano())

	fmt.Println("service started")
	poll(context.Background(), sqsClient, queueURL, &httpClient, s3Client, bucket, dynamoClient, table)
}

func poll(ctx context.Context, sqsClient *sqs.Client, queueURL string, httpClient *http.Client, s3Client *s3.Client, bucket string, dynamoClient *dynamodb.Client, table string) {

	sqsReceiveMessageInput := sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: 1, // For demo purposes let's only receive 1 message
		WaitTimeSeconds:     20,
		AttributeNames:      []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeName(sqsTypes.MessageSystemAttributeNameAWSTraceHeader)},
	}

	for {
		if ctx.Err() != nil {
			return
		}

		fmt.Println("receiving message")
		output, err := sqsClient.ReceiveMessage(ctx, &sqsReceiveMessageInput)
		if err != nil {
			fmt.Printf("error receiving sqs message: %v\n", err)
			return
		}

		if len(output.Messages) == 0 {
			continue
		}

		fmt.Printf("processing message %s\n", *output.Messages[0].MessageId)

		processMessage(ctx, httpClient, output.Messages[0], s3Client, bucket, dynamoClient, table)

		fmt.Printf("deleting message %s\n", *output.Messages[0].MessageId)

		sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
			QueueUrl:      &queueURL,
			ReceiptHandle: output.Messages[0].ReceiptHandle,
		})
	}
}

func processMessage(ctx context.Context, httpClient *http.Client, message sqsTypes.Message, s3Client *s3.Client, bucket string, dynamoClient *dynamodb.Client, table string) {
	// Extracts the Tracing information from the SQS message and injects it to the context
	ctx = propagateTraceFromSQSMessage(ctx, message)

	ctx, span := otel.GetTracerProvider().Tracer(serviceName).Start(ctx, "Process Message",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(semconv.MessagingMessageIDKey.String(*message.MessageId)),
	)
	defer span.End()

	// Demo writing to DynamoDB
	writeToDynamoDB(ctx, dynamoClient, table, *message.MessageId)

	// Demo tracing concurrent processes
	wg := &sync.WaitGroup{}
	wg.Add(2) // Add two go routines

	go func(ctx context.Context, wg *sync.WaitGroup, httpClient *http.Client) {
		defer wg.Done()
		makeDownstreamRequests(ctx, httpClient)
	}(ctx, wg, httpClient)

	go func(ctx context.Context, wg *sync.WaitGroup, s3Client *s3.Client, bucket string) {
		defer wg.Done()
		writeToS3Bucket(ctx, s3Client, bucket)
	}(ctx, wg, s3Client, bucket)

	wg.Wait()
}

func propagateTraceFromSQSMessage(ctx context.Context, msg sqsTypes.Message) context.Context {
	traceHeader := map[string]string{
		"X-Amzn-Trace-Id": msg.Attributes[string(sqsTypes.MessageSystemAttributeNameAWSTraceHeader)],
	}

	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(traceHeader))
}

func writeToDynamoDB(ctx context.Context, dynamoClient *dynamodb.Client, table string, msgID string) {
	_, err := dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &table,
		Item: map[string]dynamoTypes.AttributeValue{
			"id": &dynamoTypes.AttributeValueMemberS{Value: msgID},
		},
	})
	if err != nil {
		fmt.Printf("dynamodb put item error: %v\n", err)
	}
}

func makeDownstreamRequests(ctx context.Context, httpClient *http.Client) {
	minSleep := 1
	maxSleep := 3

	// Make some downstream calls
	urls := []string{
		"https://httpstat.us/201",
		"https://httpstat.us/302",
		"https://httpstat.us/404",
		"https://httpstat.us/418",
		"https://httpstat.us/503",
	}

	for _, url := range urls {
		sleep := rand.Intn(maxSleep-minSleep+1) + minSleep
		url += fmt.Sprintf("?sleep=%d", sleep*1000)

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := httpClient.Do(req)
		if err != nil {
			fmt.Printf("http request error for %s: %v\n", url, err)
			continue
		}
		resp.Body.Close()
	}
}

func writeToS3Bucket(ctx context.Context, s3Client *s3.Client, bucket string) {
	filename := fmt.Sprintf("%d.txt", time.Now().Unix())

	// buf := // 8192 bytes
	data := make([]byte, 1<<13)
	rand.Read(data)

	_, err := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &filename,
		Body:   bytes.NewBuffer(data),
	})
	if err != nil {
		fmt.Printf("s3 put object error: %v\n", err)
	}
}
