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

package grpc_gcp

import (
	"io"
	"context"
	"log"
	"os"
	"testing"

	spanner "google.golang.org/genproto/googleapis/spanner/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

const Target = "spanner.googleapis.com:443"
const Scope = "https://www.googleapis.com/auth/cloud-platform"
const Database = "projects/grpc-gcp/instances/sample/databases/benchmark"
const TestSQL = "select id from storage"
const TestColumnData = "payload"

func initClientConn(t *testing.T, maxSize uint32, maxStreams uint32) *grpc.ClientConn {
	keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	perRPC, err := oauth.NewServiceAccountFromFile(keyFile, Scope)
	if err != nil {
		log.Fatalf("Failed to create credentials: %v", err)
	}
	apiConfig, err := ParseApiConfig("spanner.grpc.config")
	if err != nil {
		log.Fatalf("Failed to parse api config file: %v", err)
	}
	apiConfig.GetChannelPool().MaxSize = maxSize
	apiConfig.GetChannelPool().MaxConcurrentStreamsLowWatermark = maxStreams

	gcpInt := NewGCPInterceptor(apiConfig)
	conn, err := grpc.Dial(
		Target,
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
		grpc.WithPerRPCCredentials(perRPC),
		grpc.WithBalancerName("grpc_gcp"),
		grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor),
		grpc.WithStreamInterceptor(gcpInt.GCPStreamClientInterceptor),
	)
	if err != nil {
		t.Errorf("Creation of ClientConn failed due to error: %s", err.Error())
	}
	return conn
}

func createSession(t *testing.T, client spanner.SpannerClient) *spanner.Session {
	createSessionRequest := spanner.CreateSessionRequest{
		Database: Database,
	}
	session, err := client.CreateSession(context.Background(), &createSessionRequest)
	if err != nil {
		t.Fatalf("CreateSession failed due to error: %s", err.Error())
	}
	return session
}

func deleteSession(t *testing.T, client spanner.SpannerClient, sessionName string) {
	deleteSessionRequest := spanner.DeleteSessionRequest{
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
	client := spanner.NewSpannerClient(conn)
	session := createSession(t, client)
	if len(currBalancer.affinityMap) != 1 {
		t.Errorf("CreateSession should map an affinity key with a subconn")
	}

	sessionName := session.GetName()

	getSessionRequest := spanner.GetSessionRequest{
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

func TestExecuteSql(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := spanner.NewSpannerClient(conn)
	session := createSession(t, client)
	if len(currBalancer.affinityMap) != 1 {
		t.Errorf("CreateSession should map an affinity key with a subconn")
	}

	sessionName := session.GetName()

	req := spanner.ExecuteSqlRequest{
		Session: sessionName,
		Sql:     TestSQL,
	}
	resSet, err := client.ExecuteSql(context.Background(), &req)
	if err != nil {
		t.Fatalf("ExecuteSql failed due to error: %s", err.Error())
	}
	if strVal := resSet.GetRows()[0].GetValues()[0].GetStringValue(); strVal != TestColumnData {
		t.Errorf("ExecuteSql return incorrect string value: %s, should be: %s", strVal, TestColumnData)
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
	client := spanner.NewSpannerClient(conn)
	session := createSession(t, client)
	sessionName := session.GetName()

	req := spanner.ExecuteSqlRequest{
		Session: sessionName,
		Sql:     TestSQL,
	}
	stream, err := client.ExecuteStreamingSql(context.Background(), &req)
	if err != nil {
		t.Fatalf("ExecuteStreamingSql failed due to error: %s", err.Error())
	}
	if len(currBalancer.scRefs) != 1 {
		t.Errorf("ExecuteStreamingSql should reuse the same subconn")
	}
	
	refs := getSubconnRefs()
	if refs[0].streamsCnt != 1 {
		t.Errorf("streamsCnt should be 1 after ExecuteStreamingSql, got %v", refs[0].streamsCnt)
	}

	partial, err := stream.Recv()
	if err != nil {
		t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
	} else {
		if strVal := partial.GetValues()[0].GetStringValue(); strVal != TestColumnData {
			t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, TestColumnData)
		}
	}
	if _, err = stream.Recv(); err != io.EOF {
		t.Errorf("Expected io.EOF, got %v", err)
	}

	if refs[0].streamsCnt != 0 {
		t.Errorf("streamsCnt should be 0 after stream.Recv(), got %v", refs[0].streamsCnt)
	}

	deleteSession(t, client, sessionName)
}

func TestMultipleStreamsInSameSession(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := spanner.NewSpannerClient(conn)
	session := createSession(t, client)
	sessionName := session.GetName()

	req := spanner.ExecuteSqlRequest{
		Session: sessionName,
		Sql:     TestSQL,
	}
	streams := []spanner.Spanner_ExecuteStreamingSqlClient{}
	numStreams := 2
	for i := 0; i < numStreams; i++ {
		stream, err := client.ExecuteStreamingSql(context.Background(), &req)
		streams = append(streams, stream)
		if err != nil {
			t.Fatalf("ExecuteStreamingSql failed due to error: %s", err.Error())
		}
	}
	refs := getSubconnRefs()
	if refs[0].streamsCnt != uint32(numStreams) {
		t.Errorf("streamsCnt should be %v, got %v", numStreams, refs[0].streamsCnt)
	}

	for _, stream := range streams {
		partial, err := stream.Recv()
		if err != nil {
			t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
		} else {
			if strVal := partial.GetValues()[0].GetStringValue(); strVal != TestColumnData {
				t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, TestColumnData)
			}
		}
		if _, err = stream.Recv(); err != io.EOF {
			t.Errorf("Expected io.EOF, got %v", err)
		}
	}
	if refs[0].streamsCnt != 0 {
		t.Errorf("streamsCnt should be 0, got %v", refs[0].streamsCnt)
	}

	deleteSession(t, client, sessionName)
}

func TestMultipleSessions(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := spanner.NewSpannerClient(conn)
	streams := []spanner.Spanner_ExecuteStreamingSqlClient{}
	sessions := []string{}

	numStreams := 2
	for i := 0; i < numStreams; i++ {
		session := createSession(t, client)
		sessionName := session.GetName()
		sessions = append(sessions, sessionName)
		req := spanner.ExecuteSqlRequest{
			Session: sessionName,
			Sql:     TestSQL,
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
		if ref.streamsCnt != 1 {
			t.Errorf("Each subconn should have 1 stream, got %v", ref.streamsCnt)
		}
		if ref.affinityCnt != 1 {
			t.Errorf("Each subconn should have 1 affinity, got %v", ref.affinityCnt)
		}
	}

	for _, stream := range streams {
		partial, err := stream.Recv()
		if err != nil {
			t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
		} else {
			if strVal := partial.GetValues()[0].GetStringValue(); strVal != TestColumnData {
				t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, TestColumnData)
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
		if ref.streamsCnt != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.streamsCnt)
		}
		if ref.affinityCnt != 1 {
			t.Errorf("Each subconn should have 1 affinity, got %v", ref.affinityCnt)
		}
	}

	for _, session := range sessions {
		deleteSession(t, client, session)
	}
	if len(refs) != numStreams {
		t.Errorf("Number of subconns should be %v, got %v", numStreams, len(refs))
	}
	for _, ref := range refs {
		if ref.streamsCnt != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.streamsCnt)
		}
		if ref.affinityCnt != 0 {
			t.Errorf("Each subconn should have 0 affinity, got %v", ref.affinityCnt)
		}
	}
}

func TestChannelPoolMaxSize(t *testing.T) {
	maxSize := 2
	conn := initClientConn(t, uint32(maxSize), 1)
	defer conn.Close()
	client := spanner.NewSpannerClient(conn)
	streams := []spanner.Spanner_ExecuteStreamingSqlClient{}
	sessions := []string{}

	for i := 0; i < 2 * maxSize; i++ {
		session := createSession(t, client)
		sessionName := session.GetName()
		sessions = append(sessions, sessionName)
		req := spanner.ExecuteSqlRequest{
			Session: sessionName,
			Sql:     TestSQL,
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
		if ref.streamsCnt != 2 {
			t.Errorf("Each subconn should have 2 streams, got %v", ref.streamsCnt)
		}
		if ref.affinityCnt != 2 {
			t.Errorf("Each subconn should have 2 affinity, got %v", ref.affinityCnt)
		}
	}

	for _, stream := range streams {
		partial, err := stream.Recv()
		if err != nil {
			t.Errorf("Receiving streaming results failed due to error :%s", err.Error())
		} else {
			if strVal := partial.GetValues()[0].GetStringValue(); strVal != TestColumnData {
				t.Errorf("ExecuteStreamingSql return incorrect string value: %s, should be: %s", strVal, TestColumnData)
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
		if ref.streamsCnt != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.streamsCnt)
		}
		if ref.affinityCnt != 2 {
			t.Errorf("Each subconn should have 2 affinity, got %v", ref.affinityCnt)
		}
	}

	for _, session := range sessions {
		deleteSession(t, client, session)
	}
	if len(refs) != maxSize {
		t.Errorf("Number of subconns should be %v, got %v", maxSize, len(refs))
	}
	for _, ref := range refs {
		if ref.streamsCnt != 0 {
			t.Errorf("Each subconn should have 0 stream, got %v", ref.streamsCnt)
		}
		if ref.affinityCnt != 0 {
			t.Errorf("Each subconn should have 0 affinity, got %v", ref.affinityCnt)
		}
	}
}