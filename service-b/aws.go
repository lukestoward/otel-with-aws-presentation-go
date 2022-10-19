package main

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
)

func getAWSConfig() aws.Config {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	// Instrument all AWS clients with OpenTelemetry
	otelaws.AppendMiddlewares(&cfg.APIOptions)

	return cfg
}

func newSQSClient(cfg aws.Config) *sqs.Client {
	client := sqs.NewFromConfig(cfg)
	return client
}
