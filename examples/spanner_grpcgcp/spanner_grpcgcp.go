// Test program to try Spanner client with gRPC-GCP channel pool.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"cloud.google.com/go/spanner"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
	gtransport "google.golang.org/api/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/protobuf/encoding/protojson"

	gpb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
)

const (
	// Spanner client uses a fixed-size channel pool and relies on the pool size
	// when issuing BatchCreateSessions calls to create new sessions.
	// This constant helps keep in line our replacement channel pool size
	// and what is communicated to the Spanner client.
	poolSize = 4
	endpoint = "spanner.googleapis.com:443"
	scope    = "https://www.googleapis.com/auth/cloud-platform"
)

var (
	project       = flag.String("project", "", "GCP project for Cloud Spanner.")
	instance_name = flag.String("instance", "test1", "Target instance")
	database_name = flag.String("database", "test1", "Target database")

	grpcGcpConfig = &gpb.ApiConfig{
		ChannelPool: &gpb.ChannelPoolConfig{
			// Creates a fixed-size gRPC-GCP channel pool.
			MinSize: poolSize,
			MaxSize: poolSize,
			// This option repeats(preserves) the strategy used by the Spanner
			// client to distribute BatchCreateSessions calls across channels.
			BindPickStrategy: gpb.ChannelPoolConfig_ROUND_ROBIN,
			// When issuing RPC call within Spanner session fallback to a ready
			// channel if the channel mapped to the session is not ready.
			FallbackToReady: true,
			// Establish a new connection for a channel where
			// no response/messages were received within last 1 second and
			// at least 3 RPC calls (started after the last response/message
			// received) timed out (deadline_exceeded).
			UnresponsiveDetectionMs: 1000,
			UnresponsiveCalls:       3,
		},
		// Configuration for all Spanner RPCs that create, use or remove
		// Spanner sessions. gRPC-GCP channel pool uses this configuration
		// to provide session to channel affinity. If Spanner introduces any new
		// method that creates/uses/removes sessions, it must be added here.
		Method: []*gpb.MethodConfig{
			{
				Name: []string{"/google.spanner.v1.Spanner/CreateSession"},
				Affinity: &gpb.AffinityConfig{
					Command:     gpb.AffinityConfig_BIND,
					AffinityKey: "name",
				},
			},
			{
				Name: []string{"/google.spanner.v1.Spanner/BatchCreateSessions"},
				Affinity: &gpb.AffinityConfig{
					Command:     gpb.AffinityConfig_BIND,
					AffinityKey: "session.name",
				},
			},
			{
				Name: []string{"/google.spanner.v1.Spanner/DeleteSession"},
				Affinity: &gpb.AffinityConfig{
					Command:     gpb.AffinityConfig_UNBIND,
					AffinityKey: "name",
				},
			},
			{
				Name: []string{"/google.spanner.v1.Spanner/GetSession"},
				Affinity: &gpb.AffinityConfig{
					Command:     gpb.AffinityConfig_BOUND,
					AffinityKey: "name",
				},
			},
			{
				Name: []string{
					"/google.spanner.v1.Spanner/BeginTransaction",
					"/google.spanner.v1.Spanner/Commit",
					"/google.spanner.v1.Spanner/ExecuteBatchDml",
					"/google.spanner.v1.Spanner/ExecuteSql",
					"/google.spanner.v1.Spanner/ExecuteStreamingSql",
					"/google.spanner.v1.Spanner/PartitionQuery",
					"/google.spanner.v1.Spanner/PartitionRead",
					"/google.spanner.v1.Spanner/Read",
					"/google.spanner.v1.Spanner/Rollback",
					"/google.spanner.v1.Spanner/StreamingRead",
				},
				Affinity: &gpb.AffinityConfig{
					Command:     gpb.AffinityConfig_BOUND,
					AffinityKey: "session",
				},
			},
		},
	}
)

// ConnPool wrapper for gRPC-GCP channel pool. gtransport.ConnPool is the
// interface Spanner client accepts as a replacement channel pool.
type grpcGcpConnPool struct {
	gtransport.ConnPool

	cc   *grpc.ClientConn
	size int
}

func (cp *grpcGcpConnPool) Conn() *grpc.ClientConn {
	return cp.cc
}

// Spanner client uses this function to get channel pool size.
func (cp *grpcGcpConnPool) Num() int {
	return cp.size
}

func (cp *grpcGcpConnPool) Close() error {
	return cp.cc.Close()
}

func databaseURI() string {
	return fmt.Sprintf("projects/%s/instances/%s/databases/%s", *project, *instance_name, *database_name)
}

func spannerClient() (*spanner.Client, error) {
	var perRPC credentials.PerRPCCredentials
	var err error
	keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if keyFile == "" {
		perRPC, err = oauth.NewApplicationDefault(context.Background(), scope)
	} else {
		perRPC, err = oauth.NewServiceAccountFromFile(keyFile, scope)
	}

	// Converting gRPC-GCP config to JSON because grpc.Dial only accepts JSON
	// config for configuring load balancers.
	grpcGcpJsonConfig, err := protojson.Marshal(grpcGcpConfig)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.Dial(
		// Spanner endpoint. Replace this with your custom endpoint if not using
		// the default endpoint.
		endpoint,
		// Application default or service account credentials set up above.
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
		grpc.WithPerRPCCredentials(perRPC),
		// Do not look up load balancer (gRPC-GCP) config via DNS.
		grpc.WithDisableServiceConfig(),
		// Instead use this static config.
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"loadBalancingConfig": [{"%s":%s}]}`, grpcgcp.Name, string(grpcGcpJsonConfig))),
		// gRPC-GCP interceptors required for proper operation of gRPC-GCP
		// channel pool. Add your interceptors as next arguments if needed.
		grpc.WithChainUnaryInterceptor(grpcgcp.GCPUnaryClientInterceptor),
		grpc.WithChainStreamInterceptor(grpcgcp.GCPStreamClientInterceptor),
		// Add more DialOption options if needed but make sure not to overwrite
		// the interceptors above.
	)
	if err != nil {
		return nil, err
	}

	pool := &grpcGcpConnPool{
		cc: conn,
		// Set the pool size on ConnPool to communicate it to Spanner client.
		size: poolSize,
	}

	// Create spanner client. gtransport.WithConnPool is one of the available
	// Spanner client options. Keep other Spanner-related options here if any
	// and move channel pool and dial option to the grpc.Dial above.
	return spanner.NewClient(context.Background(), databaseURI(), gtransport.WithConnPool(pool))
}

func main() {
	flag.Parse()
	client, err := spannerClient()
	if err != nil {
		log.Fatalf("Could not create Spanner client: %v", err)
	}

	stmt := spanner.Statement{
		SQL: `select 1`,
	}
	iter := client.Single().Query(context.Background(), stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		log.Fatalf("Could not execute query: %v", err)
	}

	var num int64
	if err := row.Columns(&num); err != nil {
		log.Fatalf("Could not parse result: %v", err)
	}

	// Should print "Returned: 1".
	log.Printf("Returned: %v\n", num)
}
