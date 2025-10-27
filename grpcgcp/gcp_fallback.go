/*
 *
 * Copyright 2025 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package grpcgcp

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GCPFallback is a wrapper around two gRPC client connections that provides a
// fallback mechanism from a primary to a fallback connection based on the
// error rate of the primary connection.
type GCPFallback struct {
	grpc.ClientConnInterface

	primaryConn       grpc.ClientConnInterface
	fallbackConn      grpc.ClientConnInterface
	isInFallback      *atomic.Bool
	primarySuccesses  *atomic.Uint64
	primaryFailures   *atomic.Uint64
	fallbackSuccesses *atomic.Uint64
	fallbackFailures  *atomic.Uint64

	enableFallback      bool
	errorRateThreshold  float32
	erroneousCodes      map[codes.Code]struct{}
	minFailedCalls      int
	primaryProbingFn    GCPFallbackProbeFn
	fallbackProbingFn   GCPFallbackProbeFn
	primaryChannelName  string
	fallbackChannelName string

	rateCheckTicker     *time.Ticker
	rateCheckDone       chan (struct{})
	primaryProbeTicker  *time.Ticker
	primaryProbeDone    chan (struct{})
	fallbackProbeTicker *time.Ticker
	fallbackProbeDone   chan (struct{})

	shutdown *atomic.Bool

	// OpenTelemetry metrics.
	meter          metric.Meter
	currentChannel metric.Int64ObservableUpDownCounter
	fallbackCount  metric.Int64Counter
	callStatus     metric.Int64Counter
	errorRatio     metric.Float64Gauge
}

// GCPFallbackProbeFn defines the function signature for probing a gRPC connection.
// It should return a string indicating the result of the probe.
type GCPFallbackProbeFn func(grpc.ClientConnInterface) string

// GCPFallbackOptions holds the configuration for the GCPFallback mechanism.
type GCPFallbackOptions struct {
	EnableFallback     bool
	ErrorRateThreshold float32
	ErroneousCodes     []codes.Code
	Period             time.Duration
	MinFailedCalls     int

	PrimaryProbingFn  GCPFallbackProbeFn
	FallbackProbingFn GCPFallbackProbeFn

	PrimaryProbingInterval  time.Duration
	FallbackProbingInterval time.Duration

	PrimaryChannelName  string
	FallbackChannelName string

	// MeterProvider is the OpenTelemetry meter provider.
	MeterProvider metric.MeterProvider
}

// NewGCPFallbackOptions creates a new GCPFallbackOptions with default values.
func NewGCPFallbackOptions() *GCPFallbackOptions {
	return &GCPFallbackOptions{
		EnableFallback:          true,
		ErrorRateThreshold:      1,
		ErroneousCodes:          []codes.Code{codes.DeadlineExceeded, codes.Unavailable, codes.Unauthenticated},
		Period:                  time.Minute,
		MinFailedCalls:          3,
		PrimaryProbingInterval:  time.Minute,
		FallbackProbingInterval: time.Minute * 15,
		PrimaryChannelName:      "primary",
		FallbackChannelName:     "fallback",
	}
}

// Make sure GCPFallback implements grpc.ClientConnInterface.
var _ grpc.ClientConnInterface = (*GCPFallback)(nil)

// NewGCPFallback creates a new GCPFallback instance. It takes a primary and a
// fallback connection, along with options to configure the fallback behavior.
func NewGCPFallback(primaryConn grpc.ClientConnInterface, fallbackConn grpc.ClientConnInterface, fallbackOpts *GCPFallbackOptions) (*GCPFallback, error) {

	errCodes := make(map[codes.Code]struct{})
	for _, c := range fallbackOpts.ErroneousCodes {
		errCodes[c] = struct{}{}
	}

	gcpFallback := &GCPFallback{
		primaryConn:       primaryConn,
		fallbackConn:      fallbackConn,
		isInFallback:      &atomic.Bool{},
		primarySuccesses:  &atomic.Uint64{},
		primaryFailures:   &atomic.Uint64{},
		fallbackSuccesses: &atomic.Uint64{},
		fallbackFailures:  &atomic.Uint64{},

		enableFallback:     fallbackOpts.EnableFallback,
		errorRateThreshold: fallbackOpts.ErrorRateThreshold,
		erroneousCodes:     errCodes,
		minFailedCalls:     fallbackOpts.MinFailedCalls,

		primaryProbingFn:  fallbackOpts.PrimaryProbingFn,
		fallbackProbingFn: fallbackOpts.FallbackProbingFn,

		primaryChannelName:  fallbackOpts.PrimaryChannelName,
		fallbackChannelName: fallbackOpts.FallbackChannelName,

		shutdown: &atomic.Bool{},
	}

	if fallbackOpts.MeterProvider != nil {
		gcpFallback.meter = fallbackOpts.MeterProvider.Meter("grpc-gcp-go", metric.WithInstrumentationVersion(Version))
		if err := gcpFallback.initMetrics(); err != nil {
			return nil, err
		}
	}

	gcpFallback.rateCheckDone = make(chan struct{})
	gcpFallback.rateCheckTicker = time.NewTicker(fallbackOpts.Period)
	go func() {
		for {
			select {
			case <-gcpFallback.rateCheckDone:
				return
			case <-gcpFallback.rateCheckTicker.C:
				gcpFallback.rateCheck()
			}
		}
	}()

	if fallbackOpts.PrimaryProbingFn != nil {
		gcpFallback.primaryProbeDone = make(chan struct{})
		gcpFallback.primaryProbeTicker = time.NewTicker(fallbackOpts.PrimaryProbingInterval)

		go func() {
			for {
				select {
				case <-gcpFallback.primaryProbeDone:
					return
				case <-gcpFallback.primaryProbeTicker.C:
					gcpFallback.probePrimary()
				}
			}
		}()
	}

	if fallbackOpts.FallbackProbingFn != nil {
		gcpFallback.fallbackProbeDone = make(chan struct{})
		gcpFallback.fallbackProbeTicker = time.NewTicker(fallbackOpts.FallbackProbingInterval)

		go func() {
			for {
				select {
				case <-gcpFallback.fallbackProbeDone:
					return
				case <-gcpFallback.fallbackProbeTicker.C:
					gcpFallback.probeFallback()
				}
			}
		}()
	}

	return gcpFallback, nil
}

func (f *GCPFallback) initMetrics() error {
	var err error
	f.currentChannel, err = f.meter.Int64ObservableUpDownCounter(
		"eef.current_channel",
		metric.WithDescription("1 for currently active channel, 0 otherwise."),
		metric.WithUnit("{channel}"),
		metric.WithInt64Callback(func(ctx context.Context, io metric.Int64Observer) error {
			if f.isInFallback.Load() {
				io.Observe(0, metric.WithAttributes(attribute.KeyValue{Key: "channel_name", Value: attribute.StringValue(f.primaryChannelName)}))
				io.Observe(1, metric.WithAttributes(attribute.KeyValue{Key: "channel_name", Value: attribute.StringValue(f.fallbackChannelName)}))
			} else {
				io.Observe(1, metric.WithAttributes(attribute.KeyValue{Key: "channel_name", Value: attribute.StringValue(f.primaryChannelName)}))
				io.Observe(0, metric.WithAttributes(attribute.KeyValue{Key: "channel_name", Value: attribute.StringValue(f.fallbackChannelName)}))
			}
			return nil
		}),
	)
	if err != nil {
		return err
	}
	f.fallbackCount, err = f.meter.Int64Counter(
		"eef.fallback_count",
		metric.WithDescription("Number of fallbacks occurred from one channel to another."),
		metric.WithUnit("{occurrence}"),
	)
	if err != nil {
		return err
	}
	f.callStatus, err = f.meter.Int64Counter(
		"eef.call_status",
		metric.WithDescription("Number of calls with a status and channel."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return err
	}
	f.errorRatio, err = f.meter.Float64Gauge(
		"eef.error_ratio",
		metric.WithDescription("Ratio of failed calls to total calls for a channel."),
		metric.WithUnit("1"),
	)

	return err
}

func (f *GCPFallback) rateCheck() {
	primaryErrorRate := float32(0)
	fallbackErrorRate := float32(0)

	primarySuccesses := f.primarySuccesses.Swap(0)
	promaryFailures := f.primaryFailures.Swap(0)
	if primarySuccesses+promaryFailures == 0 {
		primaryErrorRate = 0
	} else {
		primaryErrorRate = float32(promaryFailures) / float32(promaryFailures+primarySuccesses)
	}

	fallbackSuccesses := f.fallbackSuccesses.Swap(0)
	fallbackFailures := f.fallbackFailures.Swap(0)
	if fallbackSuccesses+fallbackFailures == 0 {
		fallbackErrorRate = 0
	} else {
		fallbackErrorRate = float32(fallbackFailures) / float32(fallbackFailures+fallbackSuccesses)
	}

	if f.enableFallback && primaryErrorRate >= f.errorRateThreshold && promaryFailures >= uint64(f.minFailedCalls) {
		if f.isInFallback.CompareAndSwap(false, true) {
			if f.fallbackCount != nil {
				f.fallbackCount.Add(
					context.Background(),
					1,
					metric.WithAttributes(
						attribute.String("from_channel_name", f.primaryChannelName),
						attribute.String("to_channel_name", f.fallbackChannelName),
					),
				)
			}
		}
	}

	if f.errorRatio != nil {
		f.errorRatio.Record(context.Background(), float64(primaryErrorRate), metric.WithAttributes(attribute.String("channel_name", f.primaryChannelName)))
		f.errorRatio.Record(context.Background(), float64(fallbackErrorRate), metric.WithAttributes(attribute.String("channel_name", f.fallbackChannelName)))
	}
}

func (f *GCPFallback) probePrimary() {
	// TODO: probe and report result.
}

func (f *GCPFallback) probeFallback() {
	// TODO: probe and report result.
}

// Close stops all the background goroutines and releases resources.
func (f *GCPFallback) Close() {
	if f.shutdown.Swap(true) {
		return
	}

	f.rateCheckTicker.Stop()
	close(f.rateCheckDone)

	if f.primaryProbeTicker != nil {
		f.primaryProbeTicker.Stop()
		close(f.fallbackProbeDone)
	}

	if f.fallbackProbeTicker != nil {
		f.fallbackProbeTicker.Stop()
		close(f.fallbackProbeDone)
	}
}

// Invoke performs a unary RPC and returns after the response is received
// into reply.
func (f *GCPFallback) Invoke(ctx context.Context, method string, args any, reply any, opts ...grpc.CallOption) error {
	if f.isInFallback.Load() {
		return connInvoke(ctx, f.fallbackConn, f.reportFallbackStatus, method, args, reply, opts...)
	}

	return connInvoke(ctx, f.primaryConn, f.reportPrimaryStatus, method, args, reply, opts...)
}

func (f *GCPFallback) isFailure(code codes.Code) bool {
	_, found := f.erroneousCodes[code]
	return found
}

func (f *GCPFallback) reportPrimaryStatus(ctx context.Context, code codes.Code) {
	if f.isFailure(code) {
		f.primaryFailures.Add(1)
	} else {
		f.primarySuccesses.Add(1)
	}

	if f.callStatus != nil {
		f.callStatus.Add(ctx, 1, metric.WithAttributes(
			attribute.String("channel_name", f.primaryChannelName),
			attribute.String("status_code", code.String()),
		))
	}
}

func (f *GCPFallback) reportFallbackStatus(ctx context.Context, code codes.Code) {
	if f.isFailure(code) {
		f.fallbackFailures.Add(1)
	} else {
		f.fallbackSuccesses.Add(1)
	}

	if f.callStatus != nil {
		f.callStatus.Add(ctx, 1, metric.WithAttributes(
			attribute.String("channel_name", f.fallbackChannelName),
			attribute.String("status_code", code.String()),
		))
	}
}

func connInvoke(ctx context.Context, conn grpc.ClientConnInterface, report func(context.Context, codes.Code), method string, args any, reply any, opts ...grpc.CallOption) error {
	err := conn.Invoke(ctx, method, args, reply, opts...)
	code := codes.OK
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			code = st.Code()
		} else {
			code = codes.Unknown
		}
	}
	report(ctx, code)
	return err
}

type monitoredStream struct {
	grpc.ClientStream

	ctx             context.Context
	reportRPCResult func(context.Context, codes.Code)
}

// RecvMsg blocks until a message is received or the stream is done.
func (w *monitoredStream) RecvMsg(m any) error {
	err := w.ClientStream.RecvMsg(m)
	if err == nil {
		return err
	}

	code := codes.OK
	if err != io.EOF {
		st, ok := status.FromError(err)
		if ok {
			code = st.Code()
		} else {
			code = codes.Unknown
		}
	}
	w.reportRPCResult(w.ctx, code)
	return err
}

func newMonitoredStream(ctx context.Context, s grpc.ClientStream, report func(context.Context, codes.Code)) grpc.ClientStream {
	return &monitoredStream{
		ctx:             ctx,
		ClientStream:    s,
		reportRPCResult: report,
	}
}

// NewStream begins a streaming RPC.
func (f *GCPFallback) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if f.isInFallback.Load() {
		s, err := f.fallbackConn.NewStream(ctx, desc, method, opts...)
		if err != nil {
			// TODO: maybe handle stream creation errors.
			return s, err
		}
		return newMonitoredStream(ctx, s, f.reportFallbackStatus), nil
	}

	s, err := f.primaryConn.NewStream(ctx, desc, method, opts...)
	if err != nil {
		// TODO: maybe handle stream creation errors.
		return s, err
	}
	return newMonitoredStream(ctx, s, f.reportPrimaryStatus), nil
}
