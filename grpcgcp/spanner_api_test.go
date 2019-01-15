package grpcgcp_test

import (
	"context"
	"log"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

const (
	database       = "projects/grpc-gcp/instances/sample/databases/benchmark"
	testSQL        = "select id from storage"
	testColumnData = "payload"
)

func TestSimpleQuery(t *testing.T) {
	ctx := context.Background()
	apiConfig, err := grpcgcp.ParseAPIConfig("spanner.grpc.config")
	if err != nil {
		t.Fatalf("Failed to parse api config file: %v", err)
	}
	gcpInt := grpcgcp.NewGCPInterceptor(apiConfig)
	opts := []option.ClientOption{
		option.WithGRPCDialOption(grpc.WithBalancerName(grpcgcp.Name)),
		option.WithGRPCDialOption(grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor)),
		option.WithGRPCDialOption(grpc.WithStreamInterceptor(gcpInt.GCPStreamClientInterceptor)),
	}
	client, err := spanner.NewClientWithConfig(ctx, database, spanner.ClientConfig{NumChannels: 1}, opts...)
	if err != nil {
		log.Fatalf("Failed to create client %v", err)
	}
	defer client.Close()

	stmt := spanner.Statement{SQL: testSQL}
	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		log.Fatalf("Query failed with %v", err)
	}

	var data string
	if err := row.Columns(&data); err != nil {
		log.Fatalf("Failed to parse row with %v", err)
	}
	if data != testColumnData {
		log.Fatalf("Got incorrect column data %v, want %v", data, testColumnData)
	}
}
