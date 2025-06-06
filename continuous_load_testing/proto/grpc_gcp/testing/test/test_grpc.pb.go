// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             v4.25.6
// source: grpc_gcp/testing/test.proto

package testing

import (
	context "context"
	messages "continuous_load_testing/proto/grpc/testing/messages"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	TestService_StreamedSequentialUnaryCall_FullMethodName = "/grpc_gcp.continuous_load_testing.TestService/StreamedSequentialUnaryCall"
)

// TestServiceClient is the client API for TestService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// A simple service to test the various types of RPCs and experiment with
// performance with various types of payload.
type TestServiceClient interface {
	// A bidirectional streaming RPC where messages are in sequence in the same bidi stream.
	// The client sends one request followed by one response.
	// This sequential message exchange over a bidi stream is used to benchmark
	// latency in Streamed Batching.
	StreamedSequentialUnaryCall(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[messages.SimpleRequest, messages.SimpleResponse], error)
}

type testServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewTestServiceClient(cc grpc.ClientConnInterface) TestServiceClient {
	return &testServiceClient{cc}
}

func (c *testServiceClient) StreamedSequentialUnaryCall(ctx context.Context, opts ...grpc.CallOption) (grpc.BidiStreamingClient[messages.SimpleRequest, messages.SimpleResponse], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &TestService_ServiceDesc.Streams[0], TestService_StreamedSequentialUnaryCall_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[messages.SimpleRequest, messages.SimpleResponse]{ClientStream: stream}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type TestService_StreamedSequentialUnaryCallClient = grpc.BidiStreamingClient[messages.SimpleRequest, messages.SimpleResponse]

// TestServiceServer is the server API for TestService service.
// All implementations must embed UnimplementedTestServiceServer
// for forward compatibility.
//
// A simple service to test the various types of RPCs and experiment with
// performance with various types of payload.
type TestServiceServer interface {
	// A bidirectional streaming RPC where messages are in sequence in the same bidi stream.
	// The client sends one request followed by one response.
	// This sequential message exchange over a bidi stream is used to benchmark
	// latency in Streamed Batching.
	StreamedSequentialUnaryCall(grpc.BidiStreamingServer[messages.SimpleRequest, messages.SimpleResponse]) error
	mustEmbedUnimplementedTestServiceServer()
}

// UnimplementedTestServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedTestServiceServer struct{}

func (UnimplementedTestServiceServer) StreamedSequentialUnaryCall(grpc.BidiStreamingServer[messages.SimpleRequest, messages.SimpleResponse]) error {
	return status.Errorf(codes.Unimplemented, "method StreamedSequentialUnaryCall not implemented")
}
func (UnimplementedTestServiceServer) mustEmbedUnimplementedTestServiceServer() {}
func (UnimplementedTestServiceServer) testEmbeddedByValue()                     {}

// UnsafeTestServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to TestServiceServer will
// result in compilation errors.
type UnsafeTestServiceServer interface {
	mustEmbedUnimplementedTestServiceServer()
}

func RegisterTestServiceServer(s grpc.ServiceRegistrar, srv TestServiceServer) {
	// If the following call pancis, it indicates UnimplementedTestServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&TestService_ServiceDesc, srv)
}

func _TestService_StreamedSequentialUnaryCall_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(TestServiceServer).StreamedSequentialUnaryCall(&grpc.GenericServerStream[messages.SimpleRequest, messages.SimpleResponse]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type TestService_StreamedSequentialUnaryCallServer = grpc.BidiStreamingServer[messages.SimpleRequest, messages.SimpleResponse]

// TestService_ServiceDesc is the grpc.ServiceDesc for TestService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var TestService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "grpc_gcp.continuous_load_testing.TestService",
	HandlerType: (*TestServiceServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "StreamedSequentialUnaryCall",
			Handler:       _TestService_StreamedSequentialUnaryCall_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "grpc_gcp/testing/test.proto",
}
