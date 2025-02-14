//go:generate sh grpc-proto-gen.sh
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	empty "continuous_load_testing/proto/grpc/testing/empty"
	test "continuous_load_testing/proto/grpc/testing/test"

	"continuous_load_testing/proto/grpc/testing/messages"
	mexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/balancer/grpclb" // Register the grpclb load balancing policy.
	_ "google.golang.org/grpc/balancer/rls"    // Register the RLS load balancing policy.
	"google.golang.org/grpc/credentials/google"
	"google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/stats/opentelemetry"
	_ "google.golang.org/grpc/xds/googledirectpath" // Register xDS resolver required for c2p directpath.
)

const (
	monitoredResourceName = "k8s_container"
	metricPrefix          = "directpathgrpctesting-pa.googleapis.com/client/"
)

var (
	serverAddr    = "google-c2p:///directpathgrpctesting-pa.googleapis.com"
	concurrency   = flag.Int("concurrency", 2, "Number of concurrent workers (default 1)")
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
)

type grpcGCPClientContinuousLoadTestResource struct {
	project_id     string
	location       string
	cluster_name   string
	namespace_name string
	pod_name       string
	container_name string
	resource       *resource.Resource
}

func (gclr *grpcGCPClientContinuousLoadTestResource) exporter() (metric.Exporter, error) {
	exporter, err := mexporter.New(
		mexporter.WithProjectID(gclr.project_id),
		mexporter.WithMetricDescriptorTypeFormatter(metricFormatter),
		mexporter.WithCreateServiceTimeSeries(),
		mexporter.WithMonitoredResourceDescription(monitoredResourceName, []string{"project_id", "location", "cluster_name", "namespace_name", "pod_name", "container_name"}),
	)
	if err != nil {
		return nil, fmt.Errorf("creating metrics exporter: %w", err)
	}
	log.Println("exporter done")
	return exporter, nil
}

// newGRPCLoadTestMonitoredResource initializes a new resource for the gRPC load test client.
func newGrpcLoadTestMonitoredResource(ctx context.Context, opts ...resource.Option) (*grpcGCPClientContinuousLoadTestResource, error) {
	_, err := resource.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	gclr := &grpcGCPClientContinuousLoadTestResource{
		pod_name:       getEnv("POD_NAME", ""),
		namespace_name: getEnv("NAMESPACE_NAME", ""),
		container_name: getEnv("CONTAINER_NAME", ""),
		project_id:     "directpathgrpctesting-client",
		location:       "us-west1",
		cluster_name:   "cluster-1",
	}
	// 	s := detectedAttrs.Set()
	// Add resource attributes using the dynamically fetched metadata
	gclr.resource, err = resource.New(ctx, resource.WithAttributes([]attribute.KeyValue{
		{Key: "gcp.resource_type", Value: attribute.StringValue(monitoredResourceName)},
		{Key: "project_id", Value: attribute.StringValue(gclr.project_id)},
		{Key: "location", Value: attribute.StringValue(gclr.location)},
		{Key: "cluster_name", Value: attribute.StringValue(gclr.cluster_name)},
		{Key: "namespace_name", Value: attribute.StringValue(gclr.namespace_name)},
		{Key: "pod_name", Value: attribute.StringValue(gclr.pod_name)},
		{Key: "container_name", Value: attribute.StringValue(gclr.container_name)},
	}...))
	return gclr, nil
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// setupOpenTelemetry sets up OpenTelemetry for the gRPC load test client, initializing the exporter and provider.
func setupOpenTelemetry() ([]grpc.DialOption, error) {
	ctx := context.Background()
	var exporter metric.Exporter
	gclr, err := newGrpcLoadTestMonitoredResource(ctx)
	if err != nil {
		log.Fatalf("Failed to create monitored resource: %v", err)
	}
	exporter, err = gclr.exporter()
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}
	log.Println("Created exporter.")
	meterOpts := []metric.Option{
		metric.WithResource(gclr.resource),
		metric.WithReader(metric.NewPeriodicReader(exporter, metric.WithInterval(20*time.Second))),
	}
	provider := metric.NewMeterProvider(meterOpts...)
	log.Println("provider done.")
	mo := opentelemetry.MetricsOptions{
		MeterProvider: provider,
		Metrics: stats.NewMetrics(
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
		),
		OptionalLabels: []string{"grpc.lb.locality"},
	}
	log.Println("mo done")
	opts := []grpc.DialOption{
		opentelemetry.DialOption(opentelemetry.Options{MetricsOptions: mo}),
		grpc.WithDefaultCallOptions(grpc.StaticMethodCallOption{}),
	}
	return opts, nil
}

func metricFormatter(m metricdata.Metrics) string {
	return metricPrefix + strings.ReplaceAll(string(m.Name), ".", "/")
}

// executeMethod executes the RPC call for a specific method with concurrency.
func executeMethod(methodName string, methodFunc func(context.Context, test.TestServiceClient) error, stub test.TestServiceClient) {
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			time.Sleep(1000 * time.Millisecond)
			for {
				err := methodFunc(ctx, stub)
				if err != nil {
					log.Printf("Error executing %s #%d: %v", methodName, i, err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func ExecuteEmptyCalls(ctx context.Context, tc test.TestServiceClient) error {
	_, err := tc.EmptyCall(ctx, &empty.Empty{})
	if err != nil {
		return fmt.Errorf("EmptyCall RPC failed: %v", err)
	}
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
		log.Printf("num_of_requests: %d", i)
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
		log.Printf("Received response #%d, Payload type: %s", index, t)
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
		log.Printf("num_of_requests: %d", i)
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
		log.Printf("num_of_requests: %d", i)
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

func main() {
	log.Println("DirectPath Continuous Load Testing Client Started.")
	log.Println("start to parse.")
	flag.Parse()
	log.Printf("Methods input from flag: %s", *methodsInput)
	if *methodsInput != "" {
		log.Printf("Methods input received: %s", *methodsInput)
		methodList := strings.Split(*methodsInput, ",")
		log.Printf("Parsed methods: %v", methodList)
		for _, method := range methodList {
			method = strings.TrimSpace(method)
			if _, exists := methods[method]; !exists {
				log.Fatalf("Invalid method specified: %s. Available methods are: EmptyCall, UnaryCall, StreamingInputCall, StreamingOutputCall, FullDuplexCall, HalfDuplexCall", method)
			}
			methods[method] = true
			log.Printf("Enabled method: %s", method)
		}
	} else {
		methods["HalfDuplexCall"] = true
		log.Println("No methods input received.default EmptyCall")
	}
	log.Println("Setting up OpenTelemetry...")
	opts, err := setupOpenTelemetry()
	if err != nil {
		log.Fatalf("Failed to set up OpenTelemetry: %v", err)
	}
	log.Println("OpenTelemetry setup completed.")
	opts = append(opts, grpc.WithCredentialsBundle(google.NewDefaultCredentials()))
	log.Println("Attempting to create gRPC connection...")
	conn, err := grpc.NewClient(serverAddr, opts...)
	log.Println("Connection attempt made.")
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server %v", err)
	}
	defer conn.Close()
	stub := test.NewTestServiceClient(conn)
	log.Println("gRPC client stub created.")
	for method, enabled := range methods {
		if enabled {
			log.Printf("Method enabled: %s", method)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			log.Printf("Starting goroutine #%d", i)
			if methods["EmptyCall"] {
				executeMethod("EmptyCall", ExecuteEmptyCalls, stub)
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
