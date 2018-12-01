package grpcgcp

import (
	// "fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"context"
)

// GCPUnaryClientInterceptor intercepts the execution of a unary RPC on the client using grpcgcp extension.
func GCPUnaryClientInterceptor(
	ctx context.Context,
	method string,
	req interface{},
	reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	metadata.AppendToOutgoingContext(ctx, "grcpgcp-affinitykey", "test-affinitykey")
	// fmt.Printf("GCPUnaryClientInterceptor req: %+v\n", req)
	err := invoker(ctx, method, req, reply, cc, opts...)
	return err
}