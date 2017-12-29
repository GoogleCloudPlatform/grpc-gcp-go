package fsutils

import (
	"log"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"

	admin "go-genproto/googleapis/firestore/admin/v1beta1"
	firestore "go-genproto/googleapis/firestore/v1beta1"
)

// MakeFSClient ... Create a new Firestore Client using JWT credentials
func MakeFSClient() (firestore.FirestoreClient, grpc.ClientConn) {

	keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	jwtCreds, err := oauth.NewServiceAccountFromFile(keyFile, "https://www.googleapis.com/auth/datastore")
	address := "firestore.googleapis.com:443"

	if err != nil {
		log.Fatalf("Failed to create JWT credentials: %v", err)
	}
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")), grpc.WithPerRPCCredentials(jwtCreds))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}

	client := firestore.NewFirestoreClient(conn)

	return client, *conn

}

// MakeFSAdminClient ... Create a new Firestore Admin Client using JWT credentials
func MakeFSAdminClient() (admin.FirestoreAdminClient, grpc.ClientConn) {

	keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	jwtCreds, err := oauth.NewServiceAccountFromFile(keyFile, "https://www.googleapis.com/auth/datastore")
	address := "firestore.googleapis.com:443"

	if err != nil {
		log.Fatalf("Failed to create JWT credentials: %v", err)
	}
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")), grpc.WithPerRPCCredentials(jwtCreds))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}

	client := admin.NewFirestoreAdminClient(conn)

	return client, *conn

}
