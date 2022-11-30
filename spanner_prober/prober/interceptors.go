// Package prober defines a Cloud Spanner prober with interceptors.
package prober

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const gfeT4T7prefix = "gfet4t7; dur="
const serverTimingKey = "server-timing"

var (
	expDistribution = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072, 262144, 524288}

	methodTag  = tag.MustNewKey("grpc_client_method")
	rpcTypeTag = tag.MustNewKey("rpc_type")

	t4t7Latency = stats.Int64(
		"t4t7_latency",
		"gRPC-GCP Spanner prober GFE latency",
		stats.UnitMilliseconds,
	)
	t4t7LatencyView = &view.View{
		Name:        MetricPrefix + t4t7Latency.Name(),
		Measure:     t4t7Latency,
		Aggregation: view.Distribution(expDistribution...),
		TagKeys:     []tag.Key{opNameTag, methodTag, rpcTypeTag},
	}
)

func init() {
	view.Register(t4t7LatencyView)
}

func recordLatency(ctx context.Context, method string, latency time.Duration) {
	stats.RecordWithTags(
		ctx,
		[]tag.Mutator{tag.Insert(methodTag, method)},
		t4t7Latency.M(latency.Milliseconds()),
	)
}

// parseT4T7Latency parse the headers and trailers for finding the gfet4t7 latency.
func parseT4T7Latency(headers, trailers metadata.MD) (time.Duration, error) {
	var serverTiming []string

	if len(headers[serverTimingKey]) > 0 {
		serverTiming = headers[serverTimingKey]
	} else if len(trailers[serverTimingKey]) > 0 {
		serverTiming = trailers[serverTimingKey]
	} else {
		return 0, fmt.Errorf("server-timing headers not found")
	}
	for _, entry := range serverTiming {
		if !strings.HasPrefix(entry, gfeT4T7prefix) {
			continue
		}
		durationText := strings.TrimPrefix(entry, gfeT4T7prefix)
		durationMillis, err := strconv.ParseInt(durationText, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse gfe latency: %v", err)
		}
		return time.Duration(durationMillis) * time.Millisecond, nil
	}
	return 0, fmt.Errorf("no gfe latency response available")
}

// AddGFELatencyUnaryInterceptor intercepts unary client requests (spanner.Commit, spanner.ExecuteSQL) and annotates GFE latency.
func AddGFELatencyUnaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {

	var headers, trailers metadata.MD
	opts = append(opts, grpc.Header(&headers))
	opts = append(opts, grpc.Trailer(&trailers))
	if err := invoker(ctx, method, req, reply, cc, opts...); err != nil {
		return err
	}

	if gfeLatency, err := parseT4T7Latency(headers, trailers); err == nil {
		if ctx, err := tag.New(ctx, tag.Insert(rpcTypeTag, "unary")); err == nil {
			recordLatency(ctx, method, gfeLatency)
		}
	}
	return nil
}

// AddGFELatencyStreamingInterceptor intercepts streaming requests StreamingSQL and annotates GFE latency.
func AddGFELatencyStreamingInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	cs, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		return cs, err
	}

	go func() {
		headers, err := cs.Header()
		if err != nil {
			return
		}
		trailers := cs.Trailer()
		if gfeLatency, err := parseT4T7Latency(headers, trailers); err == nil {
			if ctx, err := tag.New(ctx, tag.Insert(rpcTypeTag, "streaming")); err == nil {
				recordLatency(ctx, method, gfeLatency)
			}
		}
	}()

	return cs, nil
}
