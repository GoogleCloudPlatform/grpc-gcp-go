/*
 *
 * Copyright 2019 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package grpcgcp_test

import (
	"context"
	"log"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

const (
	database       = "projects/grpc-gcp/instances/sample/databases/benchmark"
	testSQL        = "select id from storage"
	testColumnData = "payload"
)

func initSpannerClient(t *testing.T, ctx context.Context) *spanner.Client {

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
	// NumChannels in ClientConfig represents the number of ClientConns created by grpc.
	// We should set it to 1 since we are using one clientconn with pool of subconns.
	client, err := spanner.NewClientWithConfig(ctx, database, spanner.ClientConfig{NumChannels: 1}, opts...)
	if err != nil {
		log.Fatalf("Failed to create client %v", err)
	}
	return client
}

func TestSimpleRead(t *testing.T) {
	ctx := context.Background()
	client := initSpannerClient(t, ctx)
	defer client.Close()

	stmt := spanner.Statement{SQL: testSQL}
	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		t.Fatalf("Query failed with %v", err)
	}

	var data string
	if err := row.Columns(&data); err != nil {
		t.Fatalf("Failed to parse row with %v", err)
	}
	if data != testColumnData {
		t.Fatalf("Got incorrect column data %v, want %v", data, testColumnData)
	}
}

func TestParallelRead(t *testing.T) {
	ctx := context.Background()
	// client := initSpannerClient(t, ctx)
	client, err := spanner.NewClient(ctx, database)
	defer client.Close()

	txn, err := client.BatchReadOnlyTransaction(ctx, spanner.StrongRead())
	if err != nil {
		t.Fatalf("Failed to get BatchReadOnlyTransaction with %v", err)
	}
	defer txn.Close()
	defer txn.Cleanup(ctx)

	stmt := spanner.Statement{SQL: testSQL}
	partitions, err := txn.PartitionQuery(ctx, stmt, spanner.PartitionOptions{})
	if err != nil {
		t.Fatalf("PartitionQuery failed with %v", err)
	}

	wg := sync.WaitGroup{}
	for i, p := range partitions {
		wg.Add(1)
		go func(i int, p *spanner.Partition) {
			defer wg.Done()
			iter := txn.Execute(ctx, p)
			defer iter.Stop()
			for {
				row, err := iter.Next()
				if err == iterator.Done {
					break
				} else if err != nil {
					t.Fatalf("iter.Next failed with %v", err)
				}
				var data string
				if err := row.Columns(&data); err != nil {
					t.Fatalf("Failed to parse row with %v", err)
				}
			}
		}(i, p)
	}
	wg.Wait()
}
