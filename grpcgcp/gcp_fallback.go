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
	isInFallback      bool
	primarySuccesses  *atomic.Uint64
	primaryFailures   *atomic.Uint64
	fallbackSuccesses *atomic.Uint64
	fallbackFailures  *atomic.Uint64

	enableFallback     bool
	errorRateThreshold float32
	erroneousCodes     map[codes.Code]struct{}
	minFailedCalls     int
	primaryProbingFn   GCPFallbackProbeFn
	fallbackProbingFn  GCPFallbackProbeFn

	rateCheckTicker     *time.Ticker
	rateCheckDone       chan (struct{})
	primaryProbeTicker  *time.Ticker
	primaryProbeDone    chan (struct{})
	fallbackProbeTicker *time.Ticker
	fallbackProbeDone   chan (struct{})

	shutdown *atomic.Bool
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
		isInFallback:      false,
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

		shutdown: &atomic.Bool{},
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

func (f *GCPFallback) rateCheck() {
	successes := f.primarySuccesses.Swap(0)
	failures := f.primaryFailures.Swap(0)
	if successes+failures == 0 {
		return
	}

	if !f.enableFallback {
		return
	}

	rate := float32(failures) / float32(failures+successes)

	if rate >= f.errorRateThreshold && failures >= uint64(f.minFailedCalls) {
		f.isInFallback = true
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
	if f.isInFallback {
		return connInvoke(ctx, f.fallbackConn, f.reportFallbackStatus, method, args, reply, opts...)
	}

	return connInvoke(ctx, f.primaryConn, f.reportPrimaryStatus, method, args, reply, opts...)
}

func (f *GCPFallback) isFailure(code codes.Code) bool {
	_, found := f.erroneousCodes[code]
	return found
}

func (f *GCPFallback) reportPrimaryStatus(code codes.Code) {
	if f.isFailure(code) {
		f.primaryFailures.Add(1)
		return
	}

	f.primarySuccesses.Add(1)
}

func (f *GCPFallback) reportFallbackStatus(code codes.Code) {
	if f.isFailure(code) {
		f.fallbackFailures.Add(1)
		return
	}

	f.fallbackSuccesses.Add(1)
}

func connInvoke(ctx context.Context, conn grpc.ClientConnInterface, report func(codes.Code), method string, args any, reply any, opts ...grpc.CallOption) error {
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
	report(code)
	return err
}

type monitoredStream struct {
	grpc.ClientStream

	reportRPCResult func(codes.Code)
}

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
	w.reportRPCResult(code)
	return err
}

func newMonitoredStream(s grpc.ClientStream, report func(codes.Code)) grpc.ClientStream {
	return &monitoredStream{
		ClientStream:    s,
		reportRPCResult: report,
	}
}

// NewStream begins a streaming RPC.
func (f *GCPFallback) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if f.isInFallback {
		s, err := f.fallbackConn.NewStream(ctx, desc, method, opts...)
		if err != nil {
			// TODO: maybe handle stream creation errors.
			return s, err
		}
		return newMonitoredStream(s, f.reportFallbackStatus), nil
	}

	s, err := f.primaryConn.NewStream(ctx, desc, method, opts...)
	if err != nil {
		// TODO: maybe handle stream creation errors.
		return s, err
	}
	return newMonitoredStream(s, f.reportPrimaryStatus), nil
}
