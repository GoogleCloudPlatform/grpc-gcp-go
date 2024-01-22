/*
 *
 * Copyright 2023 gRPC authors.
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

package test_grpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/multiendpoint"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	configpb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	pb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/test_grpc/helloworld/helloworld"
)

type tempIOError struct{}

func (e *tempIOError) Error() string   { return "simulated temporary IO error" }
func (e *tempIOError) Timeout() bool   { return false }
func (e *tempIOError) Temporary() bool { return true }

var (
	tempErr = &tempIOError{}
	callTO  = time.Second
	waitTO  = time.Second * 3
)

type faultyConn struct {
	es       endpointStats
	endpoint string
	conn     net.Conn

	net.Conn
}

func (f *faultyConn) faulty() bool {
	return f.es.faulty(f.endpoint)
}

func (f *faultyConn) Read(b []byte) (n int, err error) {
	if f.faulty() {
		f.conn.Close()
		return f.conn.Read(b)
	}

	return f.conn.Read(b)
}

func (f *faultyConn) Write(b []byte) (n int, err error) {
	if f.faulty() {
		f.conn.Close()
		return f.conn.Write(b)
	}

	return f.conn.Write(b)
}

func (f *faultyConn) Close() error {
	return f.conn.Close()
}

func (f *faultyConn) LocalAddr() net.Addr {
	return f.conn.LocalAddr()
}

func (f *faultyConn) RemoteAddr() net.Addr {
	return f.conn.RemoteAddr()
}

func (f *faultyConn) SetDeadline(t time.Time) error {
	return f.conn.SetDeadline(t)
}

func (f *faultyConn) SetReadDeadline(t time.Time) error {
	return f.conn.SetReadDeadline(t)
}

func (f *faultyConn) SetWriteDeadline(t time.Time) error {
	return f.conn.SetWriteDeadline(t)
}

type endpointStats map[string]bool

func (es endpointStats) faulty(e string) bool {
	return es[e] == false
}

func (es endpointStats) dialer(ctx context.Context, s string) (net.Conn, error) {
	if es.faulty(s) {
		return nil, tempErr
	}

	fConn := &faultyConn{
		endpoint: s,
		es:       es,
	}
	var err error
	fConn.conn, err = net.Dial("tcp", s)
	return fConn, err
}

// Verifies that SayHello call is successful and went through the `expectedEndpoint`.
func (tc *testingClient) SayHelloWorks(ctx context.Context, expectedEndpoint string) {
	tc.t.Helper()
	ctx, cancel := context.WithTimeout(ctx, callTO)
	defer cancel()
	var header metadata.MD
	if _, err := tc.c.SayHello(ctx, &pb.HelloRequest{Name: "world"}, grpc.Header(&header)); err != nil {
		tc.t.Fatalf("could not greet: %v", err)
	}
	if got, want := header["authority-was"][0], expectedEndpoint; got != want {
		tc.t.Fatalf("endpoint wanted %q, got %q", want, got)
	}
}

// Verifies that SayHello call fails with one of the `codes`.
func (tc *testingClient) SayHelloFails(ctx context.Context, codes ...codes.Code) {
	tc.t.Helper()
	ctx, cancel := context.WithTimeout(ctx, callTO)
	defer cancel()
	var header metadata.MD
	_, err := tc.c.SayHello(ctx, &pb.HelloRequest{Name: "world"}, grpc.Header(&header))
	for _, c := range codes {
		if status.Code(err) == c {
			return
		}
	}
	tc.t.Fatalf("SayHello() want error with codes: %v, got code: %v, got error: %v", codes, status.Code(err), err)
}

// Verifies that SayHello call either fails with one of the `codes` or succeeds via the
// `expectedEndpoint`.
// Returns error if SayHello fails with one of the `codes` and nil if SayHello succeeds.
func (tc *testingClient) SayHelloWorksOrFailsWith(ctx context.Context, expectedEndpoint string, codes ...codes.Code) error {
	tc.t.Helper()
	ctx, cancel := context.WithTimeout(ctx, callTO)
	defer cancel()
	var header metadata.MD
	_, err := tc.c.SayHello(ctx, &pb.HelloRequest{Name: "world"}, grpc.Header(&header))
	if err != nil {
		for _, c := range codes {
			if status.Code(err) == c {
				return err
			}
		}
		tc.t.Fatalf("SayHello() want error with codes: %v, got code: %v, got error: %v", codes, status.Code(err), err)
	}
	if got, want := header["authority-was"][0], expectedEndpoint; got != want {
		tc.t.Fatalf("endpoint wanted %q, got %q", want, got)
	}
	return nil
}

// Calls SayHello until it succeeds via the `expectedEndpoint` or timeouts after `to`.
// In the end verifies if SayHello succeeds via the `expectedEndpoint`.
func (tc *testingClient) SayHelloWorksWithin(ctx context.Context, expectedEndpoint string, to time.Duration) {
	tc.t.Helper()
	toCTX, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()

	type sayHelloResult struct {
		err      error
		endpoint string
	}

	c := make(chan *sayHelloResult)
	defer close(c)

	go func() {
		var header metadata.MD
		for toCTX.Err() != nil {
			ctx, cancel := context.WithTimeout(ctx, callTO)
			defer cancel()
			_, err := tc.c.SayHello(ctx, &pb.HelloRequest{Name: "world"}, grpc.Header(&header))
			c <- &sayHelloResult{
				err:      err,
				endpoint: header["authority-was"][0],
			}
			time.Sleep(time.Millisecond * 50)
		}
	}()

loop:
	for {
		select {
		case <-toCTX.Done():
			break loop
		case r := <-c:
			if r.err == nil && r.endpoint == expectedEndpoint {
				break loop
			}
		}
	}

	tc.SayHelloWorks(ctx, expectedEndpoint)
}

// Calls SayHello as long as it fails with one of the `codes` and until it succeds but not longer than `to`
// Verifies that the successful call was via the `expectedEndpoint`.
func (tc *testingClient) SayHelloFailsThenWorks(ctx context.Context, expectedEndpoint string, to time.Duration, codes ...codes.Code) {
	tc.t.Helper()
	toCTX, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()
	failedTimes := 0
	for toCTX.Err() == nil {
		time.Sleep(time.Millisecond * 20)
		ctx, cancel := context.WithTimeout(ctx, callTO)
		defer cancel()
		if err := tc.SayHelloWorksOrFailsWith(ctx, expectedEndpoint, codes...); err == nil {
			// Worked via expectedEndpoint.
			if failedTimes == 0 {
				tc.t.Fatalf("SayHello didn't fail. Expected SayHello to fail at least one time with codes %v", codes)
			}
			return
		}
		failedTimes++
	}

	tc.SayHelloWorks(ctx, expectedEndpoint)
}

type testingClient struct {
	c pb.GreeterClient
	t *testing.T
}

func TestGcpMultiEndpoint(t *testing.T) {

	lEndpoint, fEndpoint := "localhost:50051", "127.0.0.3:50051"
	newE, newE2 := "127.0.0.1:50051", "127.0.0.2:50051"

	defaultME, followerME := "default", "follower"

	// We start with leader unavailable.
	eStats := endpointStats{
		lEndpoint: false,
		fEndpoint: true,
		newE:      true,
		newE2:     true,
	}

	apiCfg := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MinSize: 3,
			MaxSize: 3,
		},
	}

	conn, err := grpcgcp.NewGcpMultiEndpoint(
		&grpcgcp.GCPMultiEndpointOptions{
			GRPCgcpConfig: apiCfg,
			MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
				defaultME: {
					Endpoints: []string{lEndpoint, fEndpoint},
				},
				followerME: {
					Endpoints: []string{fEndpoint, lEndpoint},
				},
			},
			Default: defaultME,
		},
		grpc.WithInsecure(),
		grpc.WithContextDialer(eStats.dialer),
	)

	if err != nil {
		t.Fatalf("NewMultiEndpointConn returns unexpected error: %v", err)
	}

	defer conn.Close()
	c := pb.NewGreeterClient(conn)
	tc := &testingClient{
		c: c,
		t: t,
	}

	// First call to the default endpoint will fail because the leader endpoint is unavailable.
	tc.SayHelloFails(context.Background(), codes.Unavailable)

	// But follower-first ME works from the beginning.
	fCtx := grpcgcp.NewMEContext(context.Background(), followerME)
	tc.SayHelloWorks(fCtx, fEndpoint)

	// Make sure default switched to follower in a few moments.
	tc.SayHelloWorks(context.Background(), fEndpoint)

	// Enable the leader endpoint.
	eStats[lEndpoint] = true
	// Give some time to connect. Should work through leader endpoint.
	tc.SayHelloWorksWithin(context.Background(), lEndpoint, waitTO)

	// make sure follower still uses follower endpoint.
	tc.SayHelloWorks(fCtx, fEndpoint)

	// Disable follower endpoint.
	eStats[fEndpoint] = false
	// Expect first calls (by number of channels) to follower will fail after that.
	// Give some time to detect breakage. Make sure follower switched to leader endpoint.
	tc.SayHelloFailsThenWorks(fCtx, lEndpoint, waitTO, codes.Unavailable)

	// Enable follower endpoint.
	eStats[fEndpoint] = true
	// Give some time to connect/switch. Make sure follower switched back to follower endpoint.
	tc.SayHelloWorksWithin(fCtx, fEndpoint, waitTO)

	// Add new endpoint newE to the follower ME.
	newMEs := &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{lEndpoint, fEndpoint},
			},
			followerME: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)

	// Give some time to connect. Make sure follower uses new endpoint in a few moments.
	tc.SayHelloWorksWithin(fCtx, newE, waitTO)

	// Add the same endpoint to the default ME.
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{newE, lEndpoint, fEndpoint},
			},
			followerME: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	// Make sure the new endpoint is used immediately by the default ME
	// (because it should know it is ready already).
	tc.SayHelloWorks(context.Background(), newE)

	// Rearrange endpoints in the default ME, make sure new top endpoint is used immediately.
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{fEndpoint, newE, lEndpoint},
			},
			followerME: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	tc.SayHelloWorks(context.Background(), fEndpoint)

	// Renaming follower ME.
	followerME2 := "follower2"
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{fEndpoint, newE, lEndpoint},
			},
			followerME2: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	f2Ctx := grpcgcp.NewMEContext(context.Background(), followerME2)
	tc.SayHelloWorks(f2Ctx, newE)

	// Replace follower endpoint with a new endpoint (not connected).
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{newE2, newE, lEndpoint},
			},
			followerME2: {
				Endpoints: []string{newE, newE2, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	// Give some time to connect. newE2 must be used.
	tc.SayHelloWorksWithin(context.Background(), newE2, waitTO)

	// Let the follower endpoint shutdown.
	time.Sleep(time.Second)

	// Replace new endpoints with the follower endpoint.
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{fEndpoint, lEndpoint},
			},
			followerME2: {
				Endpoints: []string{fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	// leader should be used as follower was shutdown previously.
	tc.SayHelloWorks(context.Background(), lEndpoint)
	// Give some time to connect. Follower must be used.
	tc.SayHelloWorksWithin(context.Background(), fEndpoint, waitTO)
}

func TestGcpMultiEndpointWithDelays(t *testing.T) {

	recoveryTimeout := time.Millisecond * 500
	switchingDelay := time.Millisecond * 700
	margin := time.Millisecond * 50

	lEndpoint, fEndpoint := "localhost:50051", "127.0.0.3:50051"
	newE, newE2 := "127.0.0.1:50051", "127.0.0.2:50051"

	defaultME, followerME := "default", "follower"

	// We start with leader unavailable.
	eStats := endpointStats{
		lEndpoint: false,
		fEndpoint: true,
		newE:      true,
		newE2:     true,
	}

	apiCfg := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MinSize: 3,
			MaxSize: 3,
		},
	}

	conn, err := grpcgcp.NewGcpMultiEndpoint(
		&grpcgcp.GCPMultiEndpointOptions{
			GRPCgcpConfig: apiCfg,
			MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
				defaultME: {
					Endpoints:       []string{lEndpoint, fEndpoint},
					RecoveryTimeout: recoveryTimeout,
					SwitchingDelay:  switchingDelay,
				},
				followerME: {
					Endpoints:       []string{fEndpoint, lEndpoint},
					RecoveryTimeout: recoveryTimeout,
					SwitchingDelay:  switchingDelay,
				},
			},
			Default: defaultME,
		},
		grpc.WithInsecure(),
		grpc.WithContextDialer(eStats.dialer),
	)

	if err != nil {
		t.Fatalf("NewMultiEndpointConn returns unexpected error: %v", err)
	}

	defer conn.Close()
	c := pb.NewGreeterClient(conn)
	tc := &testingClient{
		c: c,
		t: t,
	}

	start := time.Now()

	// First call to the default endpoint will fail because the leader endpoint is unavailable.
	tc.SayHelloFails(context.Background(), codes.Unavailable)

	// But follower-first ME works from the beginning.
	fCtx := grpcgcp.NewMEContext(context.Background(), followerME)
	tc.SayHelloWorks(fCtx, fEndpoint)

	// Make sure default is not switched to the follower before recovery timeout.
	time.Sleep(recoveryTimeout - time.Now().Sub(start) - margin)
	tc.SayHelloWorksOrFailsWith(context.Background(), fEndpoint, codes.Unavailable)

	// Make sure default switched to follower after recovery timeout.
	time.Sleep(recoveryTimeout - time.Now().Sub(start) + margin)
	tc.SayHelloWorks(context.Background(), fEndpoint)

	// Enable the leader endpoint.
	eStats[lEndpoint] = true
	// Give some time to connect. Should work through leader endpoint.
	tc.SayHelloWorksWithin(context.Background(), lEndpoint, 2*time.Second+switchingDelay)

	// make sure follower still uses follower endpoint.
	tc.SayHelloWorks(fCtx, fEndpoint)

	// Disable follower endpoint.
	eStats[fEndpoint] = false
	// Expect first calls (by number of channels) to follower will fail after that.
	// Give some time to detect breakage. Make sure follower switched to leader endpoint.
	tc.SayHelloFailsThenWorks(fCtx, lEndpoint, waitTO, codes.Unavailable)

	// Enable follower endpoint.
	eStats[fEndpoint] = true
	// Give some time to connect/switch. Make sure follower switched back to follower endpoint.
	tc.SayHelloWorksWithin(fCtx, fEndpoint, waitTO+recoveryTimeout)

	// add new endpoint to follower ME (first)
	newMEs := &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{lEndpoint, fEndpoint},
			},
			followerME: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)

	start = time.Now()

	// Make sure followerME has not switched to the new endpoint before switching delay.
	time.Sleep(switchingDelay - margin)
	tc.SayHelloWorks(fCtx, fEndpoint)

	// Make sure followerME has switched to the new endpoint after switching delay.
	time.Sleep(switchingDelay - time.Now().Sub(start) + margin)
	tc.SayHelloWorks(fCtx, newE)

	// Add the same endpoint to the default ME.
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{newE, lEndpoint, fEndpoint},
			},
			followerME: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)

	// Even though the new endpoint is ready switching delay must kick in here.
	start = time.Now()

	// Make sure defaultME has not switched to the new endpoint before switching delay.
	time.Sleep(switchingDelay - margin)
	tc.SayHelloWorks(context.Background(), lEndpoint)

	// Make sure followerME has switched to the new endpoint after switching delay.
	time.Sleep(switchingDelay - time.Now().Sub(start) + margin)
	tc.SayHelloWorks(context.Background(), newE)

	// Rearrange endpoints in the default ME, make sure new top endpoint is used after switching delay.
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{fEndpoint, newE, lEndpoint},
			},
			followerME: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)

	start = time.Now()

	// Make sure defaultME has not switched to the new endpoint before switching delay.
	time.Sleep(switchingDelay - margin)
	tc.SayHelloWorks(context.Background(), newE)

	// Make sure followerME has switched to the new endpoint after switching delay.
	time.Sleep(switchingDelay - time.Now().Sub(start) + margin)
	tc.SayHelloWorks(context.Background(), fEndpoint)

	// Renaming follower ME.
	followerME2 := "follower2"
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{fEndpoint, newE, lEndpoint},
			},
			followerME2: {
				Endpoints: []string{newE, fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	f2Ctx := grpcgcp.NewMEContext(context.Background(), followerME2)
	tc.SayHelloWorks(f2Ctx, newE)

	// Replace follower endpoint with a new endpoint (not connected).
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{newE2, newE, lEndpoint},
			},
			followerME2: {
				Endpoints: []string{newE, newE2, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	// newE is still used as newE2 is not connected yet.
	tc.SayHelloWorks(context.Background(), newE)
	// Give some time to connect. newE2 must be used.
	tc.SayHelloWorksWithin(context.Background(), newE2, waitTO)

	// Let the follower endpoint shutdown.
	time.Sleep(time.Second)

	// Replace new endpoints with the follower endpoint.
	newMEs = &grpcgcp.GCPMultiEndpointOptions{
		GRPCgcpConfig: apiCfg,
		MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
			defaultME: {
				Endpoints: []string{fEndpoint, lEndpoint},
			},
			followerME2: {
				Endpoints: []string{fEndpoint, lEndpoint},
			},
		},
		Default: defaultME,
	}
	conn.UpdateMultiEndpoints(newMEs)
	// leader should be used as follower was shut down previously.
	tc.SayHelloWorks(context.Background(), lEndpoint)
	// Give some time to connect and switching delay. Follower must be used.
	tc.SayHelloWorksWithin(context.Background(), fEndpoint, waitTO+switchingDelay)
}

func TestGcpMultiEndpointInstantShutdown(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Panic: %v", r)
		}
	}()

	defaultME := "default"

	apiCfg := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MinSize: 3,
			MaxSize: 3,
		},
	}

	conn, err := grpcgcp.NewGcpMultiEndpoint(
		&grpcgcp.GCPMultiEndpointOptions{
			GRPCgcpConfig: apiCfg,
			MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
				defaultME: {
					Endpoints: []string{"localhost:50051"},
				},
			},
			Default: defaultME,
		},
		grpc.WithInsecure(),
	)

	if err != nil {
		t.Fatalf("NewMultiEndpointConn returns unexpected error: %v", err)
	}

	// Closing GcpMultiEndpoint immediately should not cause panic.
	conn.Close()
}

func TestGcpMultiEndpointDialFunc(t *testing.T) {

	lEndpoint, fEndpoint := "localhost:50051", "127.0.0.3:50051"

	defaultME, followerME := "default", "follower"

	apiCfg := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MinSize: 3,
			MaxSize: 3,
		},
	}

	dialUsedFor := make(map[string]*atomic.Int32)
	dialUsedFor[lEndpoint] = &atomic.Int32{}
	dialUsedFor[fEndpoint] = &atomic.Int32{}

	conn, err := grpcgcp.NewGcpMultiEndpoint(
		&grpcgcp.GCPMultiEndpointOptions{
			GRPCgcpConfig: apiCfg,
			MultiEndpoints: map[string]*multiendpoint.MultiEndpointOptions{
				defaultME: {
					Endpoints: []string{lEndpoint, fEndpoint},
				},
				followerME: {
					Endpoints: []string{fEndpoint, lEndpoint},
				},
			},
			Default: defaultME,
			DialFunc: func(ctx context.Context, target string, dopts ...grpc.DialOption) (*grpc.ClientConn, error) {
				dialUsedFor[target].Add(1)
				return grpc.DialContext(ctx, target, dopts...)
			},
		},
		grpc.WithInsecure(),
	)

	if err != nil {
		t.Fatalf("NewMultiEndpointConn returns unexpected error: %v", err)
	}

	defer conn.Close()
	c := pb.NewGreeterClient(conn)
	tc := &testingClient{
		c: c,
		t: t,
	}

	// Make a call to make sure GCPMultiEndpoint is up and running.
	tc.SayHelloWorks(context.Background(), lEndpoint)

	if got, want := dialUsedFor[lEndpoint].Load(), int32(1); got != want {
		t.Fatalf("provided dial function was called for %q endpoint %v times, want %v times", lEndpoint, got, want)
	}

	if got, want := dialUsedFor[fEndpoint].Load(), int32(1); got != want {
		t.Fatalf("provided dial function was called for %q endpoint %v times, want %v times", fEndpoint, got, want)
	}
}
