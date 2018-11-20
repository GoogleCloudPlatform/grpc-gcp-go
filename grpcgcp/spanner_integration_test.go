package grpcgcp

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

func TestSessionManagement(t *testing.T) {
	keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	perRPC, err := oauth.NewServiceAccountFromFile(keyFile, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		log.Fatalf("Failed to create credentials: %v", err)
	}
	address := "spanner.googleapis.com:443"
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")), grpc.WithPerRPCCredentials(perRPC))
	defer conn.Close()

	client := spanner.NewSpannerClient(conn)

	createSessionRequest := spanner.CreateSessionRequest{
		Database: "projects/grpc-gcp/instances/sample/databases/benchmark",
	}

	session, err := client.CreateSession(context.Background(), &createSessionRequest)
	
	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(session.GetName())

	deleteSessionRequest := spanner.DeleteSessionRequest{
		Name: session.GetName(),
	}

	_, err = client.DeleteSession(context.Background(), &deleteSessionRequest)

	if err != nil {
		fmt.Println(err)
	}
}
