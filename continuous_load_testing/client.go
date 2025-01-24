//go:generate sh grpc-proto-gen.sh
package main

import (
	"context"
	"flag"
	"log"

	empty "continuous_load_testing/proto/grpc/testing/empty"
	test "continuous_load_testing/proto/grpc/testing/test"
	"google.golang.org/grpc"
)

var backend = "google-c2p:///directpathgrpctesting-pa.googleapis.com"

func main() {
	flag.Parse()
	conn, err := grpc.NewClient(backend)
	if err != nil {
		log.Fatalf("failed to connect: %v", backend)
	}
	defer conn.Close()
	client := test.NewTestServiceClient(conn)
	ctx := context.Background()
	_, err = client.EmptyCall(ctx, &empty.Empty{})
	if err != nil {
		log.Fatalf("EmptyCall failed")
	}
}
