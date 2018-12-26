package grpc_gcp

import (
	"fmt"
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
const TestSql = "select id from storage"
const TestColumnData = "payload"

func initClientConn(t *testing.T, maxSize uint32, maxStreams uint32) *grpc.ClientConn {
	keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	perRPC, err := oauth.NewServiceAccountFromFile(keyFile, Scope)
	if err != nil {
		log.Fatalf("Failed to create credentials: %v", err)
	}
	apiConfig := ApiConfig{
		ChannelPool: &ChannelPoolConfig{
			MaxSize: maxSize,
			MaxConcurrentStreamsLowWatermark: maxStreams,
		},
		Method: []*MethodConfig{
			&MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/CreateSession"},
				Affinity: &AffinityConfig{
					Command: AffinityConfig_BIND,
					AffinityKey: "name",
				},
			},
			&MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/GetSession"},
				Affinity: &AffinityConfig{
					Command: AffinityConfig_BOUND,
					AffinityKey: "name",
				},
			},
			&MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/DeleteSession"},
				Affinity: &AffinityConfig{
					Command: AffinityConfig_UNBIND,
					AffinityKey: "name",
				},
			},
			&MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/ExecuteSql"},
				Affinity: &AffinityConfig{
					Command: AffinityConfig_BOUND,
					AffinityKey: "session",
				},
			},
			&MethodConfig{
				Name: []string{"/google.spanner.v1.Spanner/ExecuteStreamingSql"},
				Affinity: &AffinityConfig{
					Command: AffinityConfig_BOUND,
					AffinityKey: "session",
				},
			},
		},
	}
	gcpInt := NewGCPInterceptor(apiConfig)
	conn, err := grpc.Dial(
		Target,
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
		grpc.WithPerRPCCredentials(perRPC),
		grpc.WithBalancerName("grpc_gcp"),
		grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor),
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

func TestSessionManagement(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := spanner.NewSpannerClient(conn)
	session := createSession(t, client)
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

	deleteSession(t, client, sessionName)
}

func TestExecuteSql(t *testing.T) {
	conn := initClientConn(t, 10, 1)
	defer conn.Close()
	client := spanner.NewSpannerClient(conn)
	session := createSession(t, client)
	sessionName := session.GetName()

	executeSqlReq := spanner.ExecuteSqlRequest{
		Session: sessionName,
		Sql: TestSql,
	}
	resSet, err := client.ExecuteSql(context.Background(), &executeSqlReq)
	if err != nil {
		t.Fatalf("ExecuteSql failed due to error: %s", err.Error())
	}
	if strVal := resSet.GetRows()[0].GetValues()[0].GetStringValue(); strVal != TestColumnData {
		t.Errorf("ExecuteSql return incorrect string value: %s, should be: %s", strVal, TestColumnData)
	}

	deleteSession(t, client, sessionName)
}
