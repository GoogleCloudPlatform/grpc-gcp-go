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

package grpcgcp

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"

	dbapi "cloud.google.com/go/spanner/admin/database/apiv1"
	instapi "cloud.google.com/go/spanner/admin/instance/apiv1"
	pb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	apb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	ipb "google.golang.org/genproto/googleapis/spanner/admin/instance/v1"
)

var (
	randId, _           = rand.Int(rand.Reader, big.NewInt(900000))
	skipSpanner         = os.Getenv("SKIP_SPANNER")
	gcpProjectId        = os.Getenv("GCP_PROJECT_ID")
	spannerInstanceId   = "test-instance-" + strconv.FormatInt(100000+randId.Int64(), 10)
	spannerInstancePath = fmt.Sprintf("projects/%s/instances/%s", gcpProjectId, spannerInstanceId)
	spannerDbId         = "test-db"
	database            = "projects/" + gcpProjectId + "/instances/" + spannerInstanceId + "/databases/" + spannerDbId
)

const (
	regionName      = "regional-us-east1"
	tableName       = "storage"
	column          = "id"
	testSQL         = "select id from storage"
	testColumnData  = "payload"
	setupTimeoutSec = 300
)

func TestMain(m *testing.M) {
	defer teardown()
	if err := setup(); err != nil {
		panic(fmt.Sprintf("Failed to setup: %v\n", err))
	}
	m.Run()
}

func setup() error {
	fmt.Println("Setup started.")
	if skipSpanner != "" {
		fmt.Println("Setup skipped.")
		return nil
	}
	if gcpProjectId == "" {
		panic("GCP_PROJECT_ID env variable is empty. Please provide a valid GCP_PROJECT_ID.")
	}
	fmt.Printf("Creating Spanner instance %s in project %s...\n", spannerInstanceId, gcpProjectId)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*setupTimeoutSec)
	defer cancel()

	if err := createTestInstance(ctx, os.Stdout, gcpProjectId, spannerInstanceId); err != nil {
		return fmt.Errorf("failed to create the instance: %w", err)
	}
	fmt.Println("Instance created.")

	fmt.Printf("Creating Spanner database %s...\n", database)
	if err := createTestDatabase(ctx, database); err != nil {
		return fmt.Errorf("failed to create the database: %w", err)
	}
	fmt.Println("Database created.")

	fmt.Printf("Inserting record to the test table...\n")
	if err := insertTestRecord(ctx); err != nil {
		return fmt.Errorf("failed to insert test record: %w", err)
	}
	fmt.Println("Record inserted.")

	fmt.Println("Setup ended.")
	return nil
}

func teardown() {
	fmt.Println("Teardown started.")
	if skipSpanner != "" {
		fmt.Println("Teardown skipped.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*setupTimeoutSec)
	defer cancel()
	fmt.Printf("Dropping Spanner instance %s...\n", spannerInstancePath)
	if err := dropTestInstance(ctx, spannerInstancePath); err != nil {
		fmt.Printf("Failed to drop Spanner instance: %v\n", err)
	} else {
		fmt.Println("Instance dropped.")
	}
	fmt.Println("Teardown ended.")
}

func createTestInstance(ctx context.Context, w io.Writer, projectID, instanceID string) error {
	instanceAdmin, err := instapi.NewInstanceAdminClient(ctx)
	if err != nil {
		return err
	}
	defer instanceAdmin.Close()

	created_at := strconv.FormatInt(time.Now().UTC().Unix(), 10)

	op, err := instanceAdmin.CreateInstance(ctx, &ipb.CreateInstanceRequest{
		Parent:     fmt.Sprintf("projects/%s", projectID),
		InstanceId: instanceID,
		Instance: &ipb.Instance{
			Config:      fmt.Sprintf("projects/%s/instanceConfigs/%s", projectID, regionName),
			DisplayName: instanceID,
			NodeCount:   1,
			Labels: map[string]string{
				"grpc_gcp_go_tests": "true",
				"created":           created_at,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("could not create instance %s: %v", fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID), err)
	}
	// Wait for the instance creation to finish.
	i, err := op.Wait(ctx)
	if err != nil {
		return fmt.Errorf("waiting for instance creation to finish failed: %v", err)
	}
	// The instance may not be ready to serve yet.
	if i.State != ipb.Instance_READY {
		fmt.Fprintf(w, "instance state is not READY yet. Got state %v\n", i.State)
	}
	return nil
}

func dropTestInstance(ctx context.Context, name string) error {
	instanceAdmin, err := instapi.NewInstanceAdminClient(ctx)
	if err != nil {
		return err
	}
	defer instanceAdmin.Close()
	return instanceAdmin.DeleteInstance(ctx, &ipb.DeleteInstanceRequest{Name: name})
}

func createTestDatabase(ctx context.Context, db string) error {
	matches := regexp.MustCompile("^(.*)/databases/(.*)$").FindStringSubmatch(db)
	if matches == nil || len(matches) != 3 {
		return fmt.Errorf("Invalid database id %s", db)
	}

	adminClient, err := dbapi.NewDatabaseAdminClient(ctx)
	if err != nil {
		return err
	}
	defer adminClient.Close()

	sql := fmt.Sprintf("CREATE TABLE %s (%s STRING(1024)) PRIMARY KEY(%[2]s)", tableName, column)

	op, err := adminClient.CreateDatabase(ctx, &apb.CreateDatabaseRequest{
		Parent:          matches[1],
		CreateStatement: "CREATE DATABASE `" + matches[2] + "`",
		ExtraStatements: []string{sql},
	})
	if err != nil {
		return err
	}
	if _, err := op.Wait(ctx); err != nil {
		return err
	}
	return nil
}

func insertTestRecord(ctx context.Context) error {
	client, err := spanner.NewClient(ctx, database)
	defer client.Close()
	if err != nil {
		return err
	}
	m := spanner.InsertOrUpdate(tableName, []string{column}, []interface{}{testColumnData})
	_, err = client.Apply(ctx, []*spanner.Mutation{m})
	return err
}

func initSpannerClient(t *testing.T, ctx context.Context) *spanner.Client {
	apiConfig := &pb.ApiConfig{
		ChannelPool: &pb.ChannelPoolConfig{
			MaxSize:                          10,
			MaxConcurrentStreamsLowWatermark: 1,
		},
		Method: []*pb.MethodConfig{
			{
				Name: []string{"/google.spanner.v1.Spanner/CreateSession"},
				Affinity: &pb.AffinityConfig{
					Command:     pb.AffinityConfig_BIND,
					AffinityKey: "name",
				},
			},
			{
				Name: []string{"/google.spanner.v1.Spanner/GetSession"},
				Affinity: &pb.AffinityConfig{
					Command:     pb.AffinityConfig_BOUND,
					AffinityKey: "name",
				},
			},
			{
				Name: []string{"/google.spanner.v1.Spanner/DeleteSession"},
				Affinity: &pb.AffinityConfig{
					Command:     pb.AffinityConfig_UNBIND,
					AffinityKey: "name",
				},
			},
			{
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
				Affinity: &pb.AffinityConfig{
					Command:     pb.AffinityConfig_BOUND,
					AffinityKey: "session",
				},
			},
		},
	}

	c, err := protojson.Marshal(apiConfig)
	if err != nil {
		t.Fatalf("cannot json encode config: %v", err)
	}

	opts := []option.ClientOption{
		option.WithGRPCDialOption(grpc.WithDisableServiceConfig()),
		option.WithGRPCDialOption(grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"loadBalancingConfig": [{"%s":%s}]}`, Name, string(c)))),
		option.WithGRPCDialOption(grpc.WithUnaryInterceptor(GCPUnaryClientInterceptor)),
		option.WithGRPCDialOption(grpc.WithStreamInterceptor(GCPStreamClientInterceptor)),
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
