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
	"fmt"
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

	ctx    context.Context
	cancel context.CancelFunc

	primaryConn  grpc.ClientConnInterface
	fallbackConn grpc.ClientConnInterface

	// state may be shared with other GCPFallback instances, making them fall
	// back and recover together.
	state *GCPFallbackState

	enableFallback      bool
	errorRateThreshold  float32
	erroneousCodes      map[codes.Code]struct{}
	minFailedCalls      int
	recovery            recoveryConfig
	primaryProbingFn    GCPFallbackProbeFn
	fallbackProbingFn   GCPFallbackProbeFn
	primaryChannelName  string
	fallbackChannelName string
	primaryDownSince    atomic.Pointer[time.Time]
	fallbackDownSince   atomic.Pointer[time.Time]

	// OpenTelemetry metrics.
	meter                metric.Meter
	currentChCounter     metric.Int64ObservableUpDownCounter
	fallbackCounter      metric.Int64Counter
	callCounter          metric.Int64Counter
	errorRatioGauge      metric.Float64Gauge
	probeResultCounter   metric.Int64Counter
	channelDowntimeGauge metric.Float64ObservableGauge
}

// Make sure GCPFallback implements grpc.ClientConnInterface.
var _ grpc.ClientConnInterface = (*GCPFallback)(nil)

// GCPFallbackProbeFn defines the function signature for probing a gRPC
// connection.
// It should return a string indicating the result of the probe. Empty
// string means the probe was successful.
type GCPFallbackProbeFn func(grpc.ClientConnInterface) string

// GCPFallbackOptions holds the configuration for the GCPFallback mechanism.
type GCPFallbackOptions struct {
	// EnableFallback controls whether the fallback mechanism is enabled.
	EnableFallback bool
	// ErrorRateThreshold is the threshold for the error rate of the primary connection.
	// 1.0 means 100% error rate.
	ErrorRateThreshold float32
	// ErroneousCodes is a list of error codes that are considered erroneous.
	ErroneousCodes []codes.Code
	// Period is the interval at which the error rate is checked.
	Period time.Duration
	// MinFailedCalls is the minimum number of failed calls since last check.
	MinFailedCalls int

	// PrimaryProbingFn is the probing function for the primary connection.
	// The fallback decision is not made based only on the error rate of the
	// probing RPCs, but all RPCs.
	PrimaryProbingFn GCPFallbackProbeFn
	// FallbackProbingFn is the probing function for the fallback connection.
	FallbackProbingFn GCPFallbackProbeFn

	// PrimaryProbingInterval is the interval at which the primary connection is probed.
	PrimaryProbingInterval time.Duration
	// FallbackProbingInterval is the interval at which the fallback connection is probed.
	FallbackProbingInterval time.Duration

	// PrimaryChannelName is the name of the primary channel.
	PrimaryChannelName string
	// FallbackChannelName is the name of the fallback channel.
	FallbackChannelName string

	EnableRecovery                 bool
	MinPrimaryProbeSuccessCount    int
	MinPrimaryProbeSuccessDuration time.Duration

	SharedState *GCPFallbackState

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

		EnableRecovery:                 false,
		MinPrimaryProbeSuccessCount:    10,
		MinPrimaryProbeSuccessDuration: 3 * time.Minute,
	}
}

// NewGCPFallback creates a new GCPFallback instance. It takes a primary and a
// fallback connection, along with options to configure the fallback behavior.
// GCPFallback will not close the provided connections because Close is not
// a part of ClientConnInterface, thus the caller is responsible for closing
// them properly.
func NewGCPFallback(ctx context.Context, primaryConn grpc.ClientConnInterface, fallbackConn grpc.ClientConnInterface, fallbackOpts *GCPFallbackOptions) (*GCPFallback, error) {

	if fallbackOpts.EnableRecovery && fallbackOpts.PrimaryProbingFn == nil {
		return nil, fmt.Errorf("grpcgcp: EnableRecovery requires PrimaryProbingFn, which is the only signal recovery acts on")
	}
	if fallbackOpts.MinPrimaryProbeSuccessCount < 0 {
		return nil, fmt.Errorf("grpcgcp: MinPrimaryProbeSuccessCount must be non-negative, got %d", fallbackOpts.MinPrimaryProbeSuccessCount)
	}
	if fallbackOpts.MinPrimaryProbeSuccessDuration < 0 {
		return nil, fmt.Errorf("grpcgcp: MinPrimaryProbeSuccessDuration must be non-negative, got %v", fallbackOpts.MinPrimaryProbeSuccessDuration)
	}

	errCodes := make(map[codes.Code]struct{})
	for _, c := range fallbackOpts.ErroneousCodes {
		errCodes[c] = struct{}{}
	}

	fallbackCtx, cancel := context.WithCancel(ctx)

	state := fallbackOpts.SharedState
	if state == nil {
		// Closing this GCPFallback, or cancelling ctx, closes the state it owns.
		state = NewGCPFallbackState()
		context.AfterFunc(fallbackCtx, state.Close)
	}

	gcpFallback := &GCPFallback{
		ctx:          fallbackCtx,
		cancel:       cancel,
		primaryConn:  primaryConn,
		fallbackConn: fallbackConn,
		state:        state,

		enableFallback:     fallbackOpts.EnableFallback,
		errorRateThreshold: fallbackOpts.ErrorRateThreshold,
		erroneousCodes:     errCodes,
		minFailedCalls:     fallbackOpts.MinFailedCalls,
		recovery: recoveryConfig{
			enabled:      fallbackOpts.EnableRecovery,
			minSuccesses: uint64(fallbackOpts.MinPrimaryProbeSuccessCount),
			minDuration:  fallbackOpts.MinPrimaryProbeSuccessDuration,
		},

		primaryProbingFn:  fallbackOpts.PrimaryProbingFn,
		fallbackProbingFn: fallbackOpts.FallbackProbingFn,

		primaryChannelName:  fallbackOpts.PrimaryChannelName,
		fallbackChannelName: fallbackOpts.FallbackChannelName,
	}

	if fallbackOpts.MeterProvider != nil {
		gcpFallback.meter = fallbackOpts.MeterProvider.Meter("grpc-gcp-go", metric.WithInstrumentationVersion(Version))
		if err := gcpFallback.initMetrics(); err != nil {
			cancel()
			return nil, err
		}
	}

	if fallbackOpts.EnableFallback {
		state.startRateCheck(fallbackOpts.Period, gcpFallback.rateCheck)
	}

	if fallbackOpts.PrimaryProbingFn != nil {
		go func() {
			ticker := time.NewTicker(fallbackOpts.PrimaryProbingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-gcpFallback.ctx.Done():
					return
				case <-ticker.C:
					gcpFallback.probePrimary()
				}
			}
		}()
	}

	if fallbackOpts.FallbackProbingFn != nil {
		go func() {
			ticker := time.NewTicker(fallbackOpts.FallbackProbingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-gcpFallback.ctx.Done():
					return
				case <-ticker.C:
					gcpFallback.probeFallback()
				}
			}
		}()
	}

	return gcpFallback, nil
}

func (f *GCPFallback) initMetrics() error {
	var err error
	f.currentChCounter, err = f.meter.Int64ObservableUpDownCounter(
		"eef.current_channel",
		metric.WithDescription("1 for currently active channel, 0 otherwise."),
		metric.WithUnit("{channel}"),
		metric.WithInt64Callback(func(ctx context.Context, io metric.Int64Observer) error {
			if f.state.isInFallback() {
				io.Observe(0, metric.WithAttributes(attribute.String("channel_name", f.primaryChannelName)))
				io.Observe(1, metric.WithAttributes(attribute.String("channel_name", f.fallbackChannelName)))
			} else {
				io.Observe(1, metric.WithAttributes(attribute.String("channel_name", f.primaryChannelName)))
				io.Observe(0, metric.WithAttributes(attribute.String("channel_name", f.fallbackChannelName)))
			}
			return nil
		}),
	)
	if err != nil {
		return err
	}
	f.fallbackCounter, err = f.meter.Int64Counter(
		"eef.fallback_count",
		metric.WithDescription("Number of fallbacks occurred from one channel to another."),
		metric.WithUnit("{occurrence}"),
	)
	if err != nil {
		return err
	}
	f.callCounter, err = f.meter.Int64Counter(
		"eef.call_status",
		metric.WithDescription("Number of calls with a status and channel."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return err
	}
	f.errorRatioGauge, err = f.meter.Float64Gauge(
		"eef.error_ratio",
		metric.WithDescription("Ratio of failed calls to total calls for a channel."),
		metric.WithUnit("1"),
	)
	if err != nil {
		return err
	}
	f.probeResultCounter, err = f.meter.Int64Counter(
		"eef.probe_result",
		metric.WithDescription("Results of probing functions execution."),
		metric.WithUnit("{result}"),
	)
	if err != nil {
		return err
	}
	f.channelDowntimeGauge, err = f.meter.Float64ObservableGauge(
		"eef.channel_downtime",
		metric.WithDescription("How many consecutive seconds probing fails for the channel."),
		metric.WithUnit("s"),
		metric.WithFloat64Callback(func(ctx context.Context, io metric.Float64Observer) error {
			primaryDownSince := f.primaryDownSince.Load()
			fallbackDownSince := f.fallbackDownSince.Load()
			if primaryDownSince == nil {
				io.Observe(
					0,
					metric.WithAttributes(attribute.String("channel_name", f.primaryChannelName)),
				)
			} else {
				io.Observe(
					time.Since(*primaryDownSince).Seconds(),
					metric.WithAttributes(attribute.String("channel_name", f.primaryChannelName)),
				)
			}
			if fallbackDownSince == nil {
				io.Observe(
					0,
					metric.WithAttributes(attribute.String("channel_name", f.fallbackChannelName)),
				)
			} else {
				io.Observe(
					time.Since(*fallbackDownSince).Seconds(),
					metric.WithAttributes(attribute.String("channel_name", f.fallbackChannelName)),
				)
			}
			return nil
		}),
	)

	return err
}

func (f *GCPFallback) rateCheck() {
	primaryErrorRate := float32(0)
	fallbackErrorRate := float32(0)

	// Read before draining so that a concurrent recovery invalidates these counts.
	generation := f.state.generation.Load()

	primarySuccesses, primaryFailures := f.state.drainPrimary()
	if primarySuccesses+primaryFailures == 0 {
		primaryErrorRate = 0
	} else {
		primaryErrorRate = float32(primaryFailures) / float32(primaryFailures+primarySuccesses)
	}

	fallbackSuccesses, fallbackFailures := f.state.drainFallback()
	if fallbackSuccesses+fallbackFailures == 0 {
		fallbackErrorRate = 0
	} else {
		fallbackErrorRate = float32(fallbackFailures) / float32(fallbackFailures+fallbackSuccesses)
	}

	if f.enableFallback && primaryErrorRate >= f.errorRateThreshold && primaryFailures >= uint64(f.minFailedCalls) {
		if f.state.triggerFallback(generation) {
			f.reportTransition(f.primaryChannelName, f.fallbackChannelName)
		}
	}

	if f.errorRatioGauge != nil {
		f.errorRatioGauge.Record(context.Background(), float64(primaryErrorRate), metric.WithAttributes(attribute.String("channel_name", f.primaryChannelName)))
		f.errorRatioGauge.Record(context.Background(), float64(fallbackErrorRate), metric.WithAttributes(attribute.String("channel_name", f.fallbackChannelName)))
	}
}

// reportTransition counts a switch from one channel to another.
func (f *GCPFallback) reportTransition(fromChannelName, toChannelName string) {
	if f.fallbackCounter == nil {
		return
	}
	f.fallbackCounter.Add(
		context.Background(),
		1,
		metric.WithAttributes(
			attribute.String("from_channel_name", fromChannelName),
			attribute.String("to_channel_name", toChannelName),
		),
	)
}

func (f *GCPFallback) probePrimary() {
	if !f.state.isInFallback() {
		f.primaryDownSince.Store(nil)
		return
	}

	// Read before probing so that a concurrent transition invalidates the result.
	generation := f.state.generation.Load()

	result := f.primaryProbingFn(f.primaryConn)
	if result == "" {
		f.primaryDownSince.Store(nil)
	} else {
		now := time.Now()
		f.primaryDownSince.CompareAndSwap(nil, &now)
	}

	if f.state.recordPrimaryProbeResult(result == "", generation, f.recovery) {
		f.reportTransition(f.fallbackChannelName, f.primaryChannelName)
	}

	if f.probeResultCounter != nil {
		f.probeResultCounter.Add(f.ctx, 1, metric.WithAttributes(attribute.String("channel_name", f.primaryChannelName), attribute.String("result", result)))
	}
}

func (f *GCPFallback) probeFallback() {
	result := f.fallbackProbingFn(f.fallbackConn)
	if result == "" {
		f.fallbackDownSince.Store(nil)
	} else {
		now := time.Now()
		f.fallbackDownSince.CompareAndSwap(nil, &now)
	}
	if f.probeResultCounter != nil {
		f.probeResultCounter.Add(f.ctx, 1, metric.WithAttributes(attribute.String("channel_name", f.fallbackChannelName), attribute.String("result", result)))
	}
}

// Close stops the background goroutines of this instance and releases resources.
// Another way to close GCPFallback is to cancel the provided context.
// But both ways do not close the underlying connections.
// The caller must close the primary and the fallback ClientConn on their own.
// A state provided via GCPFallbackOptions.SharedState is not closed either.
func (f *GCPFallback) Close() {
	f.cancel()
}

// Invoke performs a unary RPC and returns after the response is received
// into reply.
func (f *GCPFallback) Invoke(ctx context.Context, method string, args any, reply any, opts ...grpc.CallOption) error {
	if f.state.isInFallback() {
		err := f.fallbackConn.Invoke(ctx, method, args, reply, opts...)
		f.reportFallbackStatus(ctx, codeFromError(err))
		return err
	}

	err := f.primaryConn.Invoke(ctx, method, args, reply, opts...)
	f.reportPrimaryStatus(ctx, codeFromError(err))
	return err
}

func (f *GCPFallback) isFailure(code codes.Code) bool {
	_, found := f.erroneousCodes[code]
	return found
}

func (f *GCPFallback) addToCallCounter(ctx context.Context, channelName, status string) {
	if f.callCounter == nil {
		return
	}

	f.callCounter.Add(ctx, 1, metric.WithAttributeSet(
		attribute.NewSet(
			attribute.String("channel_name", channelName),
			attribute.String("status_code", status),
		),
	))
}

func (f *GCPFallback) reportPrimaryStatus(ctx context.Context, code codes.Code) {
	if !f.state.isInFallback() {
		f.state.recordPrimary(f.isFailure(code))
	}
	f.addToCallCounter(ctx, f.primaryChannelName, code.String())
}

func (f *GCPFallback) reportFallbackStatus(ctx context.Context, code codes.Code) {
	f.state.recordFallback(f.isFailure(code))
	f.addToCallCounter(ctx, f.fallbackChannelName, code.String())
}

func codeFromError(err error) codes.Code {
	return status.Convert(err).Code()
}

func streamingCodeFromError(err error) codes.Code {
	// io.EOF is a successful stream close while streaming.
	if err == io.EOF {
		return codes.OK
	}

	return status.Convert(err).Code()
}

type monitoredStream struct {
	grpc.ClientStream

	ctx             context.Context
	reportRPCResult func(context.Context, codes.Code)
}

// RecvMsg blocks until a message is received or the stream is done.
func (ms *monitoredStream) RecvMsg(m any) error {
	err := ms.ClientStream.RecvMsg(m)
	if err == nil {
		return err
	}

	ms.reportRPCResult(ms.ctx, streamingCodeFromError(err))
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
	if f.state.isInFallback() {
		s, err := f.fallbackConn.NewStream(ctx, desc, method, opts...)
		if err != nil {
			f.reportFallbackStatus(ctx, codeFromError(err))
			return s, err
		}
		return newMonitoredStream(ctx, s, f.reportFallbackStatus), nil
	}

	s, err := f.primaryConn.NewStream(ctx, desc, method, opts...)
	if err != nil {
		f.reportPrimaryStatus(ctx, codeFromError(err))
		return s, err
	}
	return newMonitoredStream(ctx, s, f.reportPrimaryStatus), nil
}
