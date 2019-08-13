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
	"io"
	"os"
	"sync"
	"testing"

	sppb "google.golang.org/genproto/googleapis/spanner/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

const (
	target         = "spanner.googleapis.com:443"
	scope          = "https://www.googleapis.com/auth/cloud-platform"
	database       = "projects/grpc-gcp/instances/sample/databases/benchmark"
	testSQL        = "select id from storage"
	testColumnData = "payload"
)

var currBalancer *gcpBalancer

// testBuilderWrapper wraps the real gcpBalancerBuilder
// so it can cache the created balancer object
type testBuilderWrapper struct {
	name        string
	realBuilder *gcpBalancerBuilder
}

func (tb *testBuilderWrapper) Build(
	cc balancer.ClientConn,
	opt balancer.BuildOptions,
) balancer.Balancer {
	currBalancer = tb.realBuilder.Build(cc, opt).(*gcpBalancer)
	return currBalancer
}

func (*testBuilderWrapper) Name() string {
	return Name
}

func initClientConn(t *testing.T, maxSize uint32, maxStreams uint32) *grpc.ClientConn {
	keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	perRPC, err := oauth.NewServiceAccountFromFile(keyFile, scope)
	if err != nil {
		t.Fatalf("Failed to create credentials: %v", err)
	}
	apiConfig, err := ParseAPIConfig("spanner.grpc.config")
	if err != nil {
		t.Fatalf("Failed to parse api config file: %v", err)
	}
	apiConfig.GetChannelPool().MaxSize = maxSize
	apiConfig.GetChannelPool().MaxConcurrentStreamsLowWatermark = maxStreams

	// Register test builder wrapper
	balancer.Register(&testBuilderWrapper{
		name:        Name,
		realBuilder: &gcpBalancerBuilder{name: Name},
	})

	gcpInt := NewGCPInterceptor(apiConfig)
	conn, err := grpc.Dial(
		target,
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
		grpc.WithPerRPCCredentials(perRPC),
		grpc.WithBalancerName(Name),
		grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor),
		grpc.WithStreamInterceptor(gcpInt.GCPStreamClientInterceptor),
	)
	if err != nil {
		t.Errorf("Creation of ClientConn failed due to error: %s", err.Error())
	}
	return conn
}

func createSession(t *testing.T, client sppb.SpannerClient) *sppb.Session {
	createSessionRequest := sppb.CreateSessionRequest{
		Database: database,
	}
	session, err := client.CreateSession(context.Background(), &createSessionRequest)
	if err != nil {
		t.Fatalf("CreateSession failed due to error: %s", err.Error())
	}
	return session
}

func deleteSession(t *testing.T, client sppb.SpannerClient, sessionName string) {
	deleteSessionRequest := sppb.DeleteSessionRequest{
		Name: sessionName,
	}
	_, err := client.DeleteSession(context.Background(), &deleteSessionRequest)
	if err != nil {
		t.Errorf("DeleteSession failed due to error: %s", err.Error())
	}
}

func getSubconnRefs() []*subConnRef {
	refs := []*subConnRef{}
	for _, ref := range currBalancer.scRefs {
		refs = append(refs, ref)
	}
	return refs
}

func TestSessionManagement(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	session := createSession(t, client)
	if len(currBalancer.affinityMap) != 1 {
		t.Errorf("CreateSession should map an affinity key with a subconn")
	}

	sessionName := session.GetName()

	getSessionRequest := sppb.GetSessionRequest{
		Name: sessionName,
	}
	getRes, err := client.GetSession(context.Background(), &getSessionRequest)
	if err != nil {
		t.Errorf("GetSession failed due to error: %s", err.Error())
	}
	if getRes.GetName() != sessionName {
		t.Errorf("GetSession returns different session name: %s, should be: %s", getRes.GetName(), sessionName)
	}
	if len(currBalancer.scRefs) != 1 {
		t.Errorf("GetSession should reuse the same subconn")
	}

	deleteSession(t, client, sessionName)

	if len(currBalancer.affinityMap) != 0 {
		t.Errorf("DeleteSession should remove the mapping of affinity and subconn")
	}
}

func TestUnaryCall(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	session := createSession(t, client)
	if len(currBalancer.affinityMap) != 1 {
		t.Errorf("CreateSession should map an affinity key with a subconn")
	}

	sessionName := session.GetName()

	req := sppb.ExecuteSqlRequest{
		Session: sessionName,
		Sql:     testSQL,
	}
	resSet, err := client.ExecuteSql(context.Background(), &req)
	if err != nil {
		t.Fatalf("ExecuteSql failed due to error: %s", err.Error())
	}
	if strVal := resSet.GetRows()[0].GetValues()[0].GetStringValue(); strVal != testColumnData {
		t.Errorf("ExecuteSql return incorrect string value: %s, should be: %s", strVal, testColumnData)
	}
	if len(currBalancer.scRefs) != 1 {
		t.Errorf("ExecuteSql should reuse the same subconn")
	}

	deleteSession(t, client, sessionName)
	if len(currBalancer.affinityMap) != 0 {
		t.Errorf("DeleteSession should remove the mapping of affinity and subconn")
	}
}

func TestOneStream(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	session := createSession(t, client)
	sessionName := session.GetName()

	req := sppb.ExecuteSqlRequest{
		Session: sessionName,
		Sql:     testSQL,
	}
	stream, err := client.ExecuteStreamingSql(context.Background(), &req)
	if err != nil {
		t.Fatalf("ExecuteStreamingSql failed due to error: %s", err.Error())
	}
	if len(currBalancer.scRefs) != 1 {
		t.Errorf("ExecuteStreamingSql should reuse the same subconn")
	}

	refs := getSubconnRefs()
	if refs[0].getStreamsCnt() != 1 {
		t.Errorf("streamsCnt should be 1 after ExecuteStreamingSql, got %v", refs[0].getStreamsCnt())
	}

	partial, err := stream.Recv()
	if err != nil {
		t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
	} else {
		if strVal := partial.GetValues()[0].GetStringValue(); strVal != testColumnData {
			t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, testColumnData)
		}
	}
	if _, err = stream.Recv(); err != io.EOF {
		t.Errorf("Expected io.EOF, got %v", err)
	}

	if refs[0].getStreamsCnt() != 0 {
		t.Errorf("streamsCnt should be 0 after stream.Recv(), got %v", refs[0].getStreamsCnt())
	}

	deleteSession(t, client, sessionName)
}

func TestMultipleStreamsInSameSession(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	session := createSession(t, client)
	sessionName := session.GetName()

	req := sppb.ExecuteSqlRequest{
		Session: sessionName,
		Sql:     testSQL,
	}
	streams := []sppb.Spanner_ExecuteStreamingSqlClient{}
	numStreams := 2
	for i := 0; i < numStreams; i++ {
		stream, err := client.ExecuteStreamingSql(context.Background(), &req)
		streams = append(streams, stream)
		if err != nil {
			t.Fatalf("ExecuteStreamingSql failed due to error: %s", err.Error())
		}
	}
	refs := getSubconnRefs()
	if refs[0].getStreamsCnt() != int32(numStreams) {
		t.Errorf("streamsCnt should be %v, got %v", numStreams, refs[0].getStreamsCnt())
	}

	for _, stream := range streams {
		partial, err := stream.Recv()
		if err != nil {
			t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
		} else {
			if strVal := partial.GetValues()[0].GetStringValue(); strVal != testColumnData {
				t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, testColumnData)
			}
		}
		if _, err = stream.Recv(); err != io.EOF {
			t.Errorf("Expected io.EOF, got %v", err)
		}
	}
	if refs[0].getStreamsCnt() != 0 {
		t.Errorf("streamsCnt should be 0, got %v", refs[0].getStreamsCnt())
	}

	deleteSession(t, client, sessionName)
}

func TestMultipleSessions(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	streams := []sppb.Spanner_ExecuteStreamingSqlClient{}
	sessions := []string{}

	numStreams := 2
	for i := 0; i < numStreams; i++ {
		session := createSession(t, client)
		sessionName := session.GetName()
		sessions = append(sessions, sessionName)
		req := sppb.ExecuteSqlRequest{
			Session: sessionName,
			Sql:     testSQL,
		}
		stream, err := client.ExecuteStreamingSql(context.Background(), &req)
		streams = append(streams, stream)
		if err != nil {
			t.Errorf("ExecuteStreamingSql failed due to error: %s", err.Error())
		}
	}

	refs := getSubconnRefs()
	if len(refs) != numStreams {
		t.Errorf("Number of subconns should be %v, got %v", numStreams, len(refs))
	}
	for _, ref := range refs {
		if ref.getStreamsCnt() != 1 {
			t.Errorf("Each subconn should have 1 stream, got %v", ref.getStreamsCnt())
		}
		if ref.getAffinityCnt() != 1 {
			t.Errorf("Each subconn should have 1 affinity, got %v", ref.getAffinityCnt())
		}
	}

	for _, stream := range streams {
		partial, err := stream.Recv()
		if err != nil {
			t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
		} else {
			if strVal := partial.GetValues()[0].GetStringValue(); strVal != testColumnData {
				t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, testColumnData)
			}
		}
		if _, err = stream.Recv(); err != io.EOF {
			t.Errorf("Expected io.EOF, got %v", err)
		}
	}

	if len(refs) != numStreams {
		t.Errorf("Number of subconns should be %v, got %v", numStreams, len(refs))
	}
	for _, ref := range refs {
		if ref.getStreamsCnt() != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.getStreamsCnt())
		}
		if ref.getAffinityCnt() != 1 {
			t.Errorf("Each subconn should have 1 affinity, got %v", ref.getAffinityCnt())
		}
	}

	for _, session := range sessions {
		deleteSession(t, client, session)
	}
	if len(refs) != numStreams {
		t.Errorf("Number of subconns should be %v, got %v", numStreams, len(refs))
	}
	for _, ref := range refs {
		if ref.getStreamsCnt() != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.getStreamsCnt())
		}
		if ref.getAffinityCnt() != 0 {
			t.Errorf("Each subconn should have 0 affinity, got %v", ref.getAffinityCnt())
		}
	}
}

func TestChannelPoolMaxSize(t *testing.T) {
	maxSize := 2
	conn := initClientConn(t, uint32(maxSize), 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	streams := []sppb.Spanner_ExecuteStreamingSqlClient{}
	sessions := []string{}

	for i := 0; i < 2*maxSize; i++ {
		session := createSession(t, client)
		sessionName := session.GetName()
		sessions = append(sessions, sessionName)
		req := sppb.ExecuteSqlRequest{
			Session: sessionName,
			Sql:     testSQL,
		}
		stream, err := client.ExecuteStreamingSql(context.Background(), &req)
		streams = append(streams, stream)
		if err != nil {
			t.Errorf("ExecuteStreamingSql failed due to error: %s", err.Error())
		}
	}

	refs := getSubconnRefs()
	if len(refs) != maxSize {
		t.Errorf("Number of subconns should be %v, got %v", maxSize, len(refs))
	}
	for _, ref := range refs {
		if ref.getStreamsCnt() != 2 {
			t.Errorf("Each subconn should have 2 streams, got %v", ref.getStreamsCnt())
		}
		if ref.getAffinityCnt() != 2 {
			t.Errorf("Each subconn should have 2 affinity, got %v", ref.getAffinityCnt())
		}
	}

	for _, stream := range streams {
		partial, err := stream.Recv()
		if err != nil {
			t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
		} else {
			if strVal := partial.GetValues()[0].GetStringValue(); strVal != testColumnData {
				t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, testColumnData)
			}
		}
		if _, err = stream.Recv(); err != io.EOF {
			t.Errorf("Expected io.EOF, got %v", err)
		}
	}
	if len(refs) != maxSize {
		t.Errorf("Number of subconns should be %v, got %v", maxSize, len(refs))
	}
	for _, ref := range refs {
		if ref.getStreamsCnt() != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.getStreamsCnt())
		}
		if ref.getAffinityCnt() != 2 {
			t.Errorf("Each subconn should have 2 affinity, got %v", ref.getAffinityCnt())
		}
	}

	for _, session := range sessions {
		deleteSession(t, client, session)
	}
	if len(refs) != maxSize {
		t.Errorf("Number of subconns should be %v, got %v", maxSize, len(refs))
	}
	for _, ref := range refs {
		if ref.getStreamsCnt() != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.getStreamsCnt())
		}
		if ref.getAffinityCnt() != 0 {
			t.Errorf("Each subconn should have 0 affinity, got %v", ref.getAffinityCnt())
		}
	}
}

func TestAsyncUnaryCallWithAffinity(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	session := createSession(t, client)
	sessionName := session.GetName()

	numThreads := 10
	wg := sync.WaitGroup{}
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(i int, c sppb.SpannerClient, s string) {
			defer wg.Done()
			req := sppb.ExecuteSqlRequest{
				Session: s,
				Sql:     testSQL,
			}
			resSet, err := c.ExecuteSql(context.Background(), &req)
			if err != nil {
				t.Fatalf("ExecuteSql failed due to error: %s", err.Error())
			}
			if strVal := resSet.GetRows()[0].GetValues()[0].GetStringValue(); strVal != testColumnData {
				t.Errorf("ExecuteSql return incorrect string value: %s, should be: %s", strVal, testColumnData)
			}
			if len(currBalancer.scRefs) != 1 {
				t.Errorf("ExecuteSql should reuse the same subconn")
			}
		}(i, client, sessionName)
	}
	wg.Wait()

	deleteSession(t, client, sessionName)
	if len(currBalancer.affinityMap) != 0 {
		t.Errorf("DeleteSession should remove the mapping of affinity and subconn")
	}
}

func TestAsyncUnaryCallWithNoAffinity(t *testing.T) {
	size := 10
	// Set concurrent streams soft limit to 1
	conn := initClientConn(t, uint32(size), 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)

	numThreads := 5
	wg := sync.WaitGroup{}
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(i int, c sppb.SpannerClient) {
			defer wg.Done()
			req := sppb.ListSessionsRequest{
				Database: database,
			}
			_, err := client.ListSessions(context.Background(), &req)
			if err != nil {
				t.Fatalf("ListSessions failed due to error: %s", err.Error())
			}
		}(i, client)
	}
	wg.Wait()

	if len(currBalancer.scRefs) != numThreads {
		t.Errorf("concurrent stream should create %v subconns, got %v", numThreads, len(currBalancer.scRefs))
	}

	// If numThreads are exceeding the pool size, no new subconns should be created
	numThreads = 2 * size
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(i int, c sppb.SpannerClient) {
			defer wg.Done()
			req := sppb.ListSessionsRequest{
				Database: database,
			}
			_, err := client.ListSessions(context.Background(), &req)
			if err != nil {
				t.Fatalf("ListSessions failed due to error: %s", err.Error())
			}
		}(i, client)
	}
	wg.Wait()

	if len(currBalancer.scRefs) != size {
		t.Errorf("concurrent stream should create %v subconns, got %v", size, len(currBalancer.scRefs))
	}
}

func TestAsyncStreamCallWithAffinity(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := sppb.NewSpannerClient(conn)
	session := createSession(t, client)
	sessionName := session.GetName()

	numThreads := 10
	wg := sync.WaitGroup{}
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(i int, c sppb.SpannerClient, s string) {
			defer wg.Done()
			req := sppb.ExecuteSqlRequest{
				Session: s,
				Sql:     testSQL,
			}
			stream, err := c.ExecuteStreamingSql(context.Background(), &req)
			if err != nil {
				t.Fatalf("ExecuteStreamingSql failed due to error: %s", err.Error())
			}
			for {
				partial, err := stream.Recv()
				if err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("stream.Recv failed with %v", err)
				}
				if strVal := partial.GetValues()[0].GetStringValue(); strVal != testColumnData {
					t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, testColumnData)
				}
			}
			if len(currBalancer.scRefs) != 1 {
				t.Errorf("ExecuteStreamingSql should reuse the same subconn")
			}
		}(i, client, sessionName)
	}
	wg.Wait()

	deleteSession(t, client, sessionName)
	if len(currBalancer.affinityMap) != 0 {
		t.Errorf("DeleteSession should remove the mapping of affinity and subconn")
	}
}
