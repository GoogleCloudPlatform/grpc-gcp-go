//go:generate sh grpc-proto-gen.sh
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"continuous_load_testing/proto/grpc/testing/empty"
	"continuous_load_testing/proto/grpc/testing/messages"
	"continuous_load_testing/proto/grpc/testing/test"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/alts"
	"google.golang.org/grpc/credentials/google"
	"google.golang.org/protobuf/proto"
)

var (
	serverAddr    = "google-c2p:///directpathgrpctesting-pa.googleapis.com"
	concurrency   = flag.Int("concurrency", 1, "Number of concurrent workers (default 1)")
	numOfRequests = flag.Int("num_of_requests", 10, "Total number of rpc requests to make (default 10)")
	methodsInput  = flag.String("methods", "", "Comma-separated list of methods to use (e.g., EmptyCall, UnaryCall)")

	methods = map[string]bool{
		"EmptyCall":           false,
		"UnaryCall":           false,
		"StreamingInputCall":  false,
		"StreamingOutputCall": false,
		"FullDuplexCall":      false,
		"HalfDuplexCall":      false,
	}
	Metrics = []string{
		"grpc.lb.wrr.rr_fallback",
		"grpc.lb.wrr.endpoint_weight_not_yet_usable",
		"grpc.lb.wrr.endpoint_weight_stale",
		"grpc.lb.wrr.endpoint_weights",
		"grpc.lb.rls.cache_entries",
		"grpc.lb.rls.cache_size",
		"grpc.lb.rls.default_target_picks",
		"grpc.lb.rls.target_picks",
		"grpc.lb.rls.failed_picks",
		"grpc.xds_client.connected",
		"grpc.xds_client.server_failure",
		"grpc.xds_client.resource_updates_valid",
		"grpc.xds_client.resource_updates_invalid",
		"grpc.xds_client.resources",
		"grpc.client.attempt.sent_total_compressed_message_size",
		"grpc.client.attempt.rcvd_total_compressed_message_size",
		"grpc.client.attempt.started",
		"grpc.client.attempt.duration",
		"grpc.client.call.duration",
	}
)

func main() {
	log.Println("DirectPath Continuous Load Testing Client Started.")
	flag.Parse()
	if *methodsInput != "" {
		methodList := strings.Split(*methodsInput, ",")
		for _, method := range methodList {
			method = strings.TrimSpace(method)
			if _, exists := methods[method]; !exists {
				log.Fatalf("Invalid method specified: %s. Available methods are: EmptyCall, UnaryCall, StreamingInputCall, StreamingOutputCall, FullDuplexCall, HalfDuplexCall", method)
			}
			methods[method] = true
		}
	}
	setupOpenTelemetry()
	var opts []grpc.DialOption
	altsOpts := alts.DefaultClientOptions()
	altsTC := alts.NewClientCreds(altsOpts)
	opts = append(opts, grpc.WithTransportCredentials(altsTC), grpc.WithCredentialsBundle(google.NewDefaultCredentials()), grpc.WithStatsHandler(otelgrpc.NewClientStatsHandler()))
	conn, err := grpc.NewClient(serverAddr, opts...)
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server %v", err)
	}
	defer conn.Close()
	// Create the TestService client
	stub := test.NewTestServiceClient(conn)
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if methods["EmptyCall"] {
				executeMethod("EmptyCall", ExecuteEmptyCalls, stub)
				log.Printf("EmptyUnaryCall #%d done", i)
			}
			if methods["UnaryCall"] {
				executeMethod("UnaryCall", ExecuteUnaryCalls, stub)
				log.Printf("LargeUnaryCall #%d done", i)
			}
			if methods["StreamingInputCall"] {
				executeMethod("StreamingInputCall", ExecuteStreamingInputCalls, stub)
				log.Printf("StreamingInputCall #%d done", i)
			}
			if methods["StreamingOutputCall"] {
				executeMethod("StreamingOutputCall", ExecuteStreamingOutputCalls, stub)
				log.Printf("StreamingOutputCall #%d done", i)
			}
			if methods["FullDuplexCall"] {
				executeMethod("FullDuplexCall", ExecuteFullDuplexCalls, stub)
				log.Printf("FullDuplexCall #%d done", i)
			}
			if methods["HalfDuplexCall"] {
				executeMethod("HalfDuplexCall", ExecuteHalfDuplexCalls, stub)
				log.Printf("HalfDuplexCall #%d done", i)
			}
		}(i)
	}
	wg.Wait()
	log.Println("All test cases completed.")
}

// TODO:Double check this function
func setupOpenTelemetry() {
	exporter, err := prometheus.New()
	if err != nil {
		log.Fatalf("failed to create Prometheus exporter: %v", err)
	}
	// Create a MeterProvider with Prometheus exporter
	meterProvider := metric.NewMeterProvider(metric.WithReader(exporter))
	otel.SetMeterProvider(meterProvider)
	// Set up TracerProvider with a simple batch processor
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	log.Println("OpenTelemetry setup complete")
}

// executeMethod executes the RPC call for a specific method with concurrency.
func executeMethod(methodName string, methodFunc func(context.Context, test.TestServiceClient) error, stub test.TestServiceClient) {
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			err := methodFunc(ctx, stub)
			if err != nil {
				log.Printf("Error executing %s #%d: %v", methodName, i, err)
			} else {
				log.Printf("%s #%d done", methodName, i)
			}
		}(i)
	}
	wg.Wait()
}

func ExecuteEmptyCalls(ctx context.Context, tc test.TestServiceClient) error {
	reply, err := tc.EmptyCall(ctx, &empty.Empty{})
	if err != nil {
		return fmt.Errorf("EmptyCall RPC failed: %v", err)
	}
	if !proto.Equal(&empty.Empty{}, reply) {
		return fmt.Errorf("EmptyCall receives %v, want %v", reply, empty.Empty{})
	}
	log.Println("EmptyCall completed successfully.")
	return nil
}

func ExecuteUnaryCalls(ctx context.Context, tc test.TestServiceClient) error {
	req := &messages.SimpleRequest{
		ResponseType: messages.PayloadType_COMPRESSABLE,
	}
	reply, err := tc.UnaryCall(ctx, req)
	if err != nil {
		return fmt.Errorf("UnaryCall RPC failed: ", err)
	}
	t := reply.GetPayload().GetType()
	if t != messages.PayloadType_COMPRESSABLE {
		return fmt.Errorf("got the reply with type %d; want %d", t, messages.PayloadType_COMPRESSABLE)
	}
	return nil
}

func ExecuteStreamingInputCalls(ctx context.Context, tc test.TestServiceClient) error {
	stream, err := tc.StreamingInputCall(ctx)
	if err != nil {
		return fmt.Errorf("%v.StreamingInputCall(_) = _, %v", tc, err)
	}
	for i := 0; i < *numOfRequests; i++ {
		req := &messages.StreamingInputCallRequest{}
		if err := stream.Send(req); err != nil {
			return fmt.Errorf("%v has error %v while sending %v", stream, err, req)
		}

	}
	// Receive the response after streaming all requests
	_, err = stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("%v.CloseAndRecv() got error %v, want %v", stream, err, nil)
	}
	return nil
}

func ExecuteStreamingOutputCalls(ctx context.Context, tc test.TestServiceClient) error {
	req := &messages.StreamingOutputCallRequest{
		ResponseType: messages.PayloadType_COMPRESSABLE,
	}
	stream, err := tc.StreamingOutputCall(ctx, req)
	if err != nil {
		return fmt.Errorf("%v.StreamingOutputCall(_) = _, %v", tc, err)
	}
	var rpcStatus error
	var index int
	for {
		reply, err := stream.Recv()
		if err != nil {
			rpcStatus = err
			break
		}
		t := reply.GetPayload().GetType()
		if t != messages.PayloadType_COMPRESSABLE {
			return fmt.Errorf("got the reply of type %d, want %d", t, messages.PayloadType_COMPRESSABLE)
		}
		index++
	}
	if rpcStatus != io.EOF {
		return fmt.Errorf("failed to finish the server streaming rpc: %v", rpcStatus)
	}
	return nil
}

func ExecuteFullDuplexCalls(ctx context.Context, tc test.TestServiceClient) error {
	stream, err := tc.FullDuplexCall(ctx)
	if err != nil {
		return fmt.Errorf("%v.FullDuplexCall(_) = _, %v", tc, err)
	}
	for i := 0; i < *numOfRequests; i++ {
		req := &messages.StreamingOutputCallRequest{
			ResponseType: messages.PayloadType_COMPRESSABLE,
		}
		if err = stream.Send(req); err != nil {
			return fmt.Errorf("%v has error %v while sending %v", stream, err, req)
		}
		reply, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("%v.Recv() = %v", stream, err)
		}
		t := reply.GetPayload().GetType()
		if t != messages.PayloadType_COMPRESSABLE {
			return fmt.Errorf("expected payload type %d, got %d", messages.PayloadType_COMPRESSABLE, t)
		}
	}
	if err = stream.CloseSend(); err != nil {
		return fmt.Errorf("error closing send stream: %v", err)
	}
	if _, err = stream.Recv(); err != io.EOF {
		return fmt.Errorf("stream didn't complete successfully: %v", err)
	}
	return nil
}

func ExecuteHalfDuplexCalls(ctx context.Context, tc test.TestServiceClient) error {
	stream, err := tc.HalfDuplexCall(ctx)
	if err != nil {
		return fmt.Errorf("%v.HalfDuplexCall(_) = _, %v", tc, err)
	}
	for i := 0; i < *numOfRequests; i++ {
		req := &messages.StreamingOutputCallRequest{
			ResponseType: messages.PayloadType_COMPRESSABLE,
		}
		if err = stream.Send(req); err != nil {
			return fmt.Errorf("%v has error %v while sending %v", stream, err, req)
		}
		reply, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("%v.Recv() = %v", stream, err)
		}
		t := reply.GetPayload().GetType()
		if t != messages.PayloadType_COMPRESSABLE {
			return fmt.Errorf("Got the reply of type %d, want %d", t, messages.PayloadType_COMPRESSABLE)
		}
	}
	if err = stream.CloseSend(); err != nil {
		return fmt.Errorf("%v.CloseSend() got %v, want %v", stream, err, nil)
	}
	if _, err = stream.Recv(); err != io.EOF {
		return fmt.Errorf("%v failed to complele the HalfDuplexCalls: %v", stream, err)
	}
	return nil
}
