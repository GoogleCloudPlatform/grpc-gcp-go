package test_grpc

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
	"google.golang.org/grpc"

	configpb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	pb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/test_grpc/helloworld/helloworld"
)

var (
	port               = 50051
	s                  = grpc.NewServer()
	apiConfigNoMethods = &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MaxSize:                          4,
			MaxConcurrentStreamsLowWatermark: 1,
		},
	}
	apiConfig = &configpb.ApiConfig{
		ChannelPool: apiConfigNoMethods.ChannelPool,
		Method: []*configpb.MethodConfig{
			{
				Name: []string{
					"/helloworld.Greeter/SayHello",
					"/helloworld.Greeter/InterruptedHello",
					"/helloworld.Greeter/RepeatHello",
				},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_BIND,
					AffinityKey: "message",
				},
			},
		},
	}
	tests = []struct {
		name   string
		apicfg *configpb.ApiConfig
	}{
		{
			name:   "ApiConfig with methods",
			apicfg: apiConfig,
		},
		{
			name:   "ApiConfig without methods",
			apicfg: apiConfigNoMethods,
		},
	}
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
	go func() {
		lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Fatalf("failed to listen: %v", err)
		}
		pb.RegisterGreeterServer(s, &server{})
		log.Printf("server listening at %v", lis.Addr())
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()
	fmt.Println("Setup ended.")
	return nil
}

func teardown() {
	fmt.Println("Teardown started.")
	s.Stop()
	fmt.Println("Teardown ended.")
}

type server struct {
	pb.UnimplementedGreeterServer
}

func (s *server) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	return &pb.HelloReply{Message: "Hello " + in.GetName()}, nil
}

func (s *server) RepeatHello(srv pb.Greeter_RepeatHelloServer) error {
	ctx := srv.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := srv.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			log.Printf("receive error %v", err)
			continue
		}

		if err := srv.Send(&pb.HelloReply{Message: "Hello " + req.GetName()}); err != nil {
			log.Printf("send error %v", err)
		}
	}
}

func (s *server) InterruptedHello(srv pb.Greeter_InterruptedHelloServer) error {
	ctx := srv.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Receive data from stream and close the stream immediately.
		srv.Recv()
		return nil
	}
}

func getConn(config *configpb.ApiConfig) (*grpc.ClientConn, error) {
	gcpInt := grpcgcp.NewGCPInterceptor(config)
	opts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBalancerName(grpcgcp.Name),
		grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor),
		grpc.WithStreamInterceptor(gcpInt.GCPStreamClientInterceptor),
	}
	return grpc.Dial("localhost:50051", opts...)
}

func TestUnaryCall(t *testing.T) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn, err := getConn(test.apicfg)
			if err != nil {
				t.Fatalf("did not connect: %v", err)
			}
			defer conn.Close()
			c := pb.NewGreeterClient(conn)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r, err := c.SayHello(ctx, &pb.HelloRequest{Name: "world"})
			if err != nil {
				t.Fatalf("could not greet: %v", err)
			}
			if r.GetMessage() != "Hello world" {
				t.Errorf("Expected Hello World, got %v", r.GetMessage())
			}
		})
	}
}

func TestStreamingCall(t *testing.T) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn, err := getConn(test.apicfg)
			if err != nil {
				t.Fatalf("did not connect: %v", err)
			}
			defer conn.Close()
			c := pb.NewGreeterClient(conn)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			rhc, err := c.RepeatHello(ctx)
			if err != nil {
				t.Fatalf("could not start stream for RepeatHello: %v", err)
			}

			rhc.Send(&pb.HelloRequest{Name: "stream"})

			r, err := rhc.Recv()
			if err != nil {
				t.Fatalf("could not get reply: %v", err)
			}

			if r.GetMessage() != "Hello stream" {
				t.Errorf("Expected Hello stream, got %v", r.GetMessage())
			}

			if err := rhc.CloseSend(); err != nil {
				t.Fatalf("could not CloseSend: %v", err)
			}
		})
	}
}
