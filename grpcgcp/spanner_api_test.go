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
	"fmt"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
	configpb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

const (
	database       = "projects/grpc-gcp/instances/sample/databases/benchmark"
	tableName      = "storage"
	column         = "id"
	testSQL        = "select id from storage"
	testColumnData = "payload"
)

func initSpannerClient(t *testing.T, ctx context.Context) *spanner.Client {
	apiConfig := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MaxSize:                          10,
			MaxConcurrentStreamsLowWatermark: 1,
		},
		Method: []*configpb.MethodConfig{
			&configpb.MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/CreateSession"},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_BIND,
					AffinityKey: "name",
				},
			},
			&configpb.MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/GetSession"},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_BOUND,
					AffinityKey: "name",
				},
			},
			&configpb.MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/DeleteSession"},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_UNBIND,
					AffinityKey: "name",
				},
			},
			&configpb.MethodConfig{
				Name: []string{
					"/google.spanner.v1.Spanner/ExecuteSql",
					"/google.spanner.v1.Spanner/ExecuteStreamingSql",
					"/google.spanner.v1.Spanner/Read",
					"/google.spanner.v1.Spanner/StreamingRead",
					"/google.spanner.v1.Spanner/BeginTransaction",
					"/google.spanner.v1.Spanner/Commit",
					"/google.spanner.v1.Spanner/Rollback",
					"/google.spanner.v1.Spanner/PartitionQuery",
					"/google.spanner.v1.Spanner/PartitionRead",
				},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_BOUND,
					AffinityKey: "session",
				},
			},
		},
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
		t.Fatalf("Failed to create client %v", err)
	}
	return client
}

func TestReadRow(t *testing.T) {
	ctx := context.Background()
	client := initSpannerClient(t, ctx)
	defer client.Close()

	r, err := client.Single().ReadRow(ctx, tableName, spanner.Key{testColumnData}, []string{column})
	if err != nil {
		t.Fatalf("ReadRow failed with %v", err)
	}
	var data string
	if err := r.Column(0, &data); err != nil {
		t.Fatalf("Failed to read column from row with %v", err)
	}
	if data != testColumnData {
		t.Fatalf("Got incorrect column data %v, want %v", data, testColumnData)
	}
}

func TestReadRowIterator(t *testing.T) {
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
	client := initSpannerClient(t, ctx)
	defer client.Close()

	stmt := spanner.Statement{SQL: testSQL}
	numThreads := 10
	wg := sync.WaitGroup{}
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(i int, c *spanner.Client) {
			defer wg.Done()
			iter := c.Single().Query(ctx, stmt)
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
		}(i, client)
	}
	wg.Wait()
}

func TestMutations(t *testing.T) {
	ctx := context.Background()
	client := initSpannerClient(t, ctx)
	defer client.Close()

	inserted := "inserted-data"
	_, err := client.Apply(ctx, []*spanner.Mutation{
		spanner.Insert(tableName, []string{column}, []interface{}{inserted}),
	})
	if err != nil {
		t.Fatalf("Insert row failed with %v", err)
	}

	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.Delete(tableName, spanner.Key{inserted}),
	})
	if err != nil {
		t.Fatalf("Delete row failed with %v", err)
	}
}

func TestParallelMutations(t *testing.T) {
	ctx := context.Background()
	client := initSpannerClient(t, ctx)
	defer client.Close()

	numThreads := 10
	wg := sync.WaitGroup{}
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(i int, c *spanner.Client) {
			defer wg.Done()
			inserted := fmt.Sprintf("inserted-%d", i)
			_, err := c.Apply(ctx, []*spanner.Mutation{
				spanner.Insert(tableName, []string{column}, []interface{}{inserted}),
				spanner.Delete(tableName, spanner.Key{inserted}),
			})
			if err != nil {
				t.Fatalf("Insert row failed with %v", err)
			}
		}(i, client)
	}
	wg.Wait()
}
