/*
 *
 * Copyright 2019 gRPC authors.
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
	"sync"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
)

func TestGCPUnaryClientInterceptor(t *testing.T) {
	ctx := context.TODO()
	wantMethod := "someMethod"
	wantReq := "requestMessage"
	wantRepl := "replyMessage"
	wantGCPCtx := &gcpContext{
		reqMsg:   wantReq,
		replyMsg: wantRepl,
	}
	wantCC := &grpc.ClientConn{}
	wantOpts := []grpc.CallOption{grpc.CallContentSubtype("someSubtype"), grpc.MaxCallRecvMsgSize(42)}

	invCalled := false
	var gotCtx context.Context
	gotMethod := ""
	var gotReq, gotRepl interface{}
	var gotCC *grpc.ClientConn
	gotOpts := []grpc.CallOption{}
	inv := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		invCalled = true
		gotCtx = ctx
		gotMethod = method
		gotReq = req
		gotRepl = reply
		gotCC = cc
		gotOpts = opts
		return nil
	}

	if err := GCPUnaryClientInterceptor(ctx, wantMethod, wantReq, wantRepl, wantCC, inv, wantOpts...); err != nil {
		t.Fatalf("GCPUnaryClientInterceptor(...) returned error: %v, want: nil", err)
	}
	if !invCalled {
		t.Fatalf("provided grpc.UnaryInvoker function was not called")
	}
	gotGCPCtx, hasGCPCtx := gotCtx.Value(gcpKey).(*gcpContext)
	if !hasGCPCtx {
		t.Errorf("provided grpc.UnaryInvoker function was called with context without gcpContext")
	} else if diff := cmp.Diff(wantGCPCtx, gotGCPCtx, cmp.AllowUnexported(gcpContext{})); diff != "" {
		t.Errorf("provided grpc.UnaryInvoker function was called with unexpected gcpContext (-want, +got):\n%s", diff)
	}
	if gotMethod != wantMethod {
		t.Errorf("provided grpc.UnaryInvoker function was called with unexpected method: %s, want: %s", gotMethod, wantMethod)
	}
	if gotReq != wantReq {
		t.Errorf("provided grpc.UnaryInvoker function was called with unexpected request: %s, want: %s", gotReq, wantReq)
	}
	if gotRepl != wantRepl {
		t.Errorf("provided grpc.UnaryInvoker function was called with unexpected response: %s, want: %s", gotRepl, wantRepl)
	}
	if gotCC != wantCC {
		t.Errorf("provided grpc.UnaryInvoker function was called with unexpected ClientConn: %v, want: %v", gotCC, wantCC)
	}
	if diff := cmp.Diff(wantOpts, gotOpts); diff != "" {
		t.Errorf("provided grpc.UnaryInvoker function was called with unexpected options (-want, +got):\n%s", diff)
	}
}

type strictMatcher struct {
	gomock.Matcher

	matchWith interface{}
}

func (sm *strictMatcher) Matches(x interface{}) bool {
	return sm.matchWith == x
}

func (sm *strictMatcher) String() string {
	return fmt.Sprintf("at address %p: %v", &sm.matchWith, sm.matchWith)
}

type fakeResp struct{}

func TestGCPStreamClientInterceptor(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.TODO()
	wantMethod := "someMethod"
	wantReq := "someRequest"
	wantRes := &fakeResp{}
	wantGCPCtx := &gcpContext{
		reqMsg: wantReq,
	}
	wantSD := &grpc.StreamDesc{}
	wantCC := &grpc.ClientConn{}
	wantOpts := []grpc.CallOption{grpc.CallContentSubtype("someSubtype"), grpc.MaxCallRecvMsgSize(42)}

	streamerCalled := false
	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		streamerCalled = true
		gotGCPCtx, hasGCPCtx := ctx.Value(gcpKey).(*gcpContext)
		if !hasGCPCtx {
			t.Errorf("grpc.Streamer called with context without gcpContext")
		} else if diff := cmp.Diff(wantGCPCtx, gotGCPCtx, cmp.AllowUnexported(gcpContext{})); diff != "" {
			t.Errorf("grpc.Streamer called with unexpected gcpContext (-want, +got):\n%s", diff)
		}
		if desc != wantSD {
			t.Errorf("grpc.Streamer called with unexpected StreamDesc: %v, want: %v", desc, wantSD)
		}
		if cc != wantCC {
			t.Errorf("grpc.Streamer called with unexpected ClientConn: %v, want: %v", cc, wantCC)
		}
		if method != wantMethod {
			t.Errorf("grpc.Streamer called with unexpected method: %v, want: %v", method, wantMethod)
		}
		if diff := cmp.Diff(wantOpts, opts); diff != "" {
			t.Errorf("grpc.Streamer called with unexpected options (-want, +got):\n%s", diff)
		}
		mockCS := mocks.NewMockClientStream(mockCtrl)
		mockCS.EXPECT().SendMsg(gomock.Eq(wantReq)).Times(1)
		mockCS.EXPECT().RecvMsg(&strictMatcher{matchWith: wantRes}).Times(1)
		return mockCS, nil
	}
	cs, err := GCPStreamClientInterceptor(
		ctx,
		wantSD,
		wantCC,
		wantMethod,
		streamer,
		wantOpts...,
	)
	if err != nil {
		t.Fatalf("GCPStreamClientInterceptor(...) returned error: %v, want: nil", err)
	}
	if streamerCalled {
		t.Fatalf("GCPStreamClientInterceptor(...) unexpectedly called grpc.Streamer on init")
	}
	if err := cs.SendMsg(wantReq); err != nil {
		t.Fatalf("SendMsg(wantReq) returned error: %v, want: nil", err)
	}
	if !streamerCalled {
		t.Fatalf("SendMsg(wantReq) must have been called grpc.Streamer")
	}
	if err := cs.RecvMsg(wantRes); err != nil {
		t.Fatalf("RecvMsg() returned error: %v, want: nil", err)
	}
}

func TestGCPStreamClientInterceptorCallingReadBeforeSend(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.TODO()
	wantMethod := "someMethod"
	wantReq := "someRequest"
	wantRes := &fakeResp{}
	wantGCPCtx := &gcpContext{
		reqMsg: wantReq,
	}
	wantSD := &grpc.StreamDesc{}
	wantCC := &grpc.ClientConn{}
	wantOpts := []grpc.CallOption{grpc.CallContentSubtype("someSubtype"), grpc.MaxCallRecvMsgSize(42)}

	streamerCalled := false
	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		streamerCalled = true
		gotGCPCtx, hasGCPCtx := ctx.Value(gcpKey).(*gcpContext)
		if !hasGCPCtx {
			t.Errorf("grpc.Streamer called with context without gcpContext")
		} else if diff := cmp.Diff(wantGCPCtx, gotGCPCtx, cmp.AllowUnexported(gcpContext{})); diff != "" {
			t.Errorf("grpc.Streamer called with unexpected gcpContext (-want, +got):\n%s", diff)
		}
		if desc != wantSD {
			t.Errorf("grpc.Streamer called with unexpected StreamDesc: %v, want: %v", desc, wantSD)
		}
		if cc != wantCC {
			t.Errorf("grpc.Streamer called with unexpected ClientConn: %v, want: %v", cc, wantCC)
		}
		if method != wantMethod {
			t.Errorf("grpc.Streamer called with unexpected method: %v, want: %v", method, wantMethod)
		}
		if diff := cmp.Diff(wantOpts, opts); diff != "" {
			t.Errorf("grpc.Streamer called with unexpected options (-want, +got):\n%s", diff)
		}
		mockCS := mocks.NewMockClientStream(mockCtrl)
		mockCS.EXPECT().SendMsg(gomock.Eq(wantReq)).Times(1)
		mockCS.EXPECT().RecvMsg(&strictMatcher{matchWith: wantRes}).Times(1)
		return mockCS, nil
	}
	cs, err := GCPStreamClientInterceptor(
		ctx,
		wantSD,
		wantCC,
		wantMethod,
		streamer,
		wantOpts...,
	)
	if err != nil {
		t.Fatalf("GCPStreamClientInterceptor(...) returned error: %v, want: nil", err)
	}
	if streamerCalled {
		t.Fatalf("GCPStreamClientInterceptor(...) unexpectedly called grpc.Streamer on init")
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	received := &sync.WaitGroup{}
	received.Add(1)
	go func() {
		wg.Done()
		if err := cs.RecvMsg(wantRes); err != nil {
			t.Errorf("RecvMsg() returned error: %v, want: nil", err)
		}
		received.Done()
	}()
	wg.Wait()
	time.Sleep(time.Millisecond)
	if err := cs.SendMsg(wantReq); err != nil {
		t.Fatalf("SendMsg(wantReq) returned error: %v, want: nil", err)
	}
	if !streamerCalled {
		t.Fatalf("SendMsg(wantReq) must have been called grpc.Streamer")
	}
	received.Wait()
}
