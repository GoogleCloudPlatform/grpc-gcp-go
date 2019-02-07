/*
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

package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"time"

	"google.golang.org/api/iterator"

	spanner "cloud.google.com/go/spanner/apiv1"
	spannerpb "google.golang.org/genproto/googleapis/spanner/v1"
)

const (
	database     = "projects/grpc-prober-testing/instances/test-instance/databases/test-db"
	table        = "users"
	testUsername = "test_username"
)

func createClient() *spanner.Client {
	ctx := context.Background()
	client, _ := spanner.NewClient(ctx)
	if client == nil {
		log.Fatal("Fail to create the client.")
		os.Exit(1)
	}
	return client
}

func sessionManagementProber(client *spanner.Client, metrics map[string]int64) error {
	ctx := context.Background()
	reqCreate := &spannerpb.CreateSessionRequest{
		Database: database,
	}
	start := time.Now()
	session, err := client.CreateSession(ctx, reqCreate)
	if err != nil {
		return err
	}
	if session == nil {
		return errors.New("failded to create a new session")
	}
	metrics["create_session_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	// DeleteSession
	defer func() {
		start = time.Now()
		reqDelete := &spannerpb.DeleteSessionRequest{
			Name: session.Name,
		}
		client.DeleteSession(ctx, reqDelete)
		metrics["delete_session_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)
	}()

	// GetSession
	reqGet := &spannerpb.GetSessionRequest{
		Name: session.Name,
	}
	start = time.Now()
	respGet, err := client.GetSession(ctx, reqGet)
	if err != nil {
		return err
	}
	if reqGet == nil || respGet.Name != session.Name {
		return errors.New("fail to get the session")
	}
	metrics["get_session_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	// ListSessions
	reqList := &spannerpb.ListSessionsRequest{
		Database: database,
	}
	start = time.Now()
	it := client.ListSessions(ctx, reqList)
	inList := false
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return err
		}
		if resp.Name == session.Name {
			inList = true
			break
		}
	}
	metrics["list_sessions_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)
	if !inList {
		return errors.New("list sessions failed")
	}
	return nil
}

func executeSqlProber(client *spanner.Client, metrics map[string]int64) error {
	ctx := context.Background()
	session := createSession(client)
	defer deleteSession(client, session)
	reqSql := &spannerpb.ExecuteSqlRequest{
		Sql:     "select * FROM " + table,
		Session: session.Name,
	}

	// ExecuteSql
	start := time.Now()
	respSql, err1 := client.ExecuteSql(ctx, reqSql)
	if err1 != nil {
		return err1
	}
	if respSql == nil || len(respSql.Rows) != 1 || respSql.Rows[0].Values[0].GetStringValue() != testUsername {
		return errors.New("execute sql failed")
	}
	metrics["execute_sql_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	// ExecuteStreamingSql
	start = time.Now()
	stream, err2 := client.ExecuteStreamingSql(ctx, reqSql)
	if err2 != nil {
		return err2
	}
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if resp == nil || resp.Values[0].GetStringValue() != testUsername {
			return errors.New("execute streaming sql failed")
		}
	}
	metrics["execute_streaming_sql_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)
	return nil
}

func readProber(client *spanner.Client, metrics map[string]int64) error {
	ctx := context.Background()
	session := createSession(client)
	defer deleteSession(client, session)
	reqRead := &spannerpb.ReadRequest{
		Session: session.Name,
		Table:   table,
		KeySet: &spannerpb.KeySet{
			All: true,
		},
		Columns: []string{"username", "firstname", "lastname"},
	}

	// Read
	start := time.Now()
	respRead, err1 := client.Read(ctx, reqRead)
	if err1 != nil {
		return err1
	}
	if respRead == nil || len(respRead.Rows) != 1 || respRead.Rows[0].Values[0].GetStringValue() != testUsername {
		return errors.New("execute read failed")
	}
	metrics["read_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	// StreamingRead
	start = time.Now()
	stream, err2 := client.StreamingRead(ctx, reqRead)
	if err2 != nil {
		return err2
	}
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if resp == nil || resp.Values[0].GetStringValue() != testUsername {
			return errors.New("streaming read failed")
		}
	}
	metrics["streaming_read_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)
	return nil
}

func transactionProber(client *spanner.Client, metrics map[string]int64) error {
	ctx := context.Background()
	session := createSession(client)
	reqBegin := &spannerpb.BeginTransactionRequest{
		Session: session.Name,
		Options: &spannerpb.TransactionOptions{
			Mode: &spannerpb.TransactionOptions_ReadWrite_{
				ReadWrite: &spannerpb.TransactionOptions_ReadWrite{},
			},
		},
	}

	// BeginTransaction
	start := time.Now()
	txn, err1 := client.BeginTransaction(ctx, reqBegin)
	if err1 != nil {
		return err1
	}
	metrics["begin_transaction_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	// Commit
	reqCommit := &spannerpb.CommitRequest{
		Session: session.Name,
		Transaction: &spannerpb.CommitRequest_TransactionId{
			TransactionId: txn.Id,
		},
	}
	start = time.Now()
	_, err2 := client.Commit(ctx, reqCommit)
	if err2 != nil {
		return err2
	}
	metrics["commit_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	// Rollback
	txn, err1 = client.BeginTransaction(ctx, reqBegin)
	if err1 != nil {
		return err1
	}
	reqRollback := &spannerpb.RollbackRequest{
		Session:       session.Name,
		TransactionId: txn.Id,
	}
	start = time.Now()
	err2 = client.Rollback(ctx, reqRollback)
	if err2 != nil {
		return err2
	}
	metrics["rollback_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	reqDelete := &spannerpb.DeleteSessionRequest{
		Name: session.Name,
	}
	client.DeleteSession(ctx, reqDelete)
	return nil
}

func partitionProber(client *spanner.Client, metrics map[string]int64) error {
	ctx := context.Background()
	session := createSession(client)
	defer deleteSession(client, session)
	selector := &spannerpb.TransactionSelector{
		Selector: &spannerpb.TransactionSelector_Begin{
			Begin: &spannerpb.TransactionOptions{
				Mode: &spannerpb.TransactionOptions_ReadOnly_{
					ReadOnly: &spannerpb.TransactionOptions_ReadOnly{},
				},
			},
		},
	}

	// PartitionQuery
	reqQuery := &spannerpb.PartitionQueryRequest{
		Session:     session.Name,
		Sql:         "select * FROM " + table,
		Transaction: selector,
	}
	start := time.Now()
	_, err := client.PartitionQuery(ctx, reqQuery)
	if err != nil {
		return err
	}
	metrics["partition_query_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)

	// PartitionRead
	reqRead := &spannerpb.PartitionReadRequest{
		Session: session.Name,
		Table:   table,
		KeySet: &spannerpb.KeySet{
			All: true,
		},
		Columns:     []string{"username", "firstname", "lastname"},
		Transaction: selector,
	}
	start = time.Now()
	_, err = client.PartitionRead(ctx, reqRead)
	if err != nil {
		return err
	}
	metrics["partition_read_latency_ms"] = int64(time.Now().Sub(start) / time.Millisecond)
	return nil
}

func createSession(client *spanner.Client) *spannerpb.Session {
	ctx := context.Background()
	reqCreate := &spannerpb.CreateSessionRequest{
		Database: database,
	}
	session, err := client.CreateSession(ctx, reqCreate)
	if err != nil {
		log.Fatal(err.Error())
		return nil
	}
	if session == nil {
		log.Fatal("Failded to create a new session.")
		return nil
	}
	return session
}

func deleteSession(client *spanner.Client, session *spannerpb.Session) {
	if client == nil {
		return
	}
	ctx := context.Background()
	reqDelete := &spannerpb.DeleteSessionRequest{
		Name: session.Name,
	}
	client.DeleteSession(ctx, reqDelete)
}
