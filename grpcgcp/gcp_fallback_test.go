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
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockCtrl struct {
	*gomock.Controller
}

func setup(t *testing.T, opts *GCPFallbackOptions) (*mockCtrl, *GCPFallback, *mocks.MockClientConnInterface, *mocks.MockClientConnInterface) {
	t.Helper()
	ctrl := gomock.NewController(t)
	primaryConn := mocks.NewMockClientConnInterface(ctrl)
	fallbackConn := mocks.NewMockClientConnInterface(ctrl)

	if opts == nil {
		opts = NewGCPFallbackOptions()
		opts.Period = 10 * time.Millisecond
		opts.ErrorRateThreshold = 0.5
		opts.MinFailedCalls = 3
	}

	gcpFallback, err := NewGCPFallback(primaryConn, fallbackConn, opts)
	if err != nil {
		t.Fatalf("NewGCPFallback() error = %v", err)
	}
	t.Cleanup(gcpFallback.Close)

	return &mockCtrl{ctrl}, gcpFallback, primaryConn, fallbackConn
}

// Expect a unary call to be made on a connection and make this call return the error code.
func expectUnary(t *testing.T, conn *mocks.MockClientConnInterface, code codes.Code) *gomock.Call {
	t.Helper()

	if code == codes.OK {
		return conn.EXPECT().Invoke(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil)
	}

	return conn.EXPECT().Invoke(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(status.Error(code, code.String()))
}

// Expect a streaming call to be made on a connection and make this call return the error code.
func (mc *mockCtrl) expectStreaming(t *testing.T, conn *mocks.MockClientConnInterface, code codes.Code) *gomock.Call {
	t.Helper()

	if code == codes.OK {
		return conn.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
				mockStream := mocks.NewMockClientStream(mc.Controller)
				mockStream.EXPECT().RecvMsg(gomock.Any()).Return(io.EOF)
				return mockStream, nil
			})
	}

	return conn.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			mockStream := mocks.NewMockClientStream(mc.Controller)
			mockStream.EXPECT().RecvMsg(gomock.Any()).Return(status.Error(code, code.String()))
			return mockStream, nil
		})
}

func TestGCPFallback_FallbackHappens(t *testing.T) {
	ctrl, gcpFallback, primaryConn, fallbackConn := setup(t, nil)

	// Prepare 3 failures, 2 successes. Rate = 3/5 = 0.6 >= 0.5. Failures = 3 >= 3.
	// Fallback should be triggered.
	expectUnary(t, primaryConn, codes.Unavailable).Times(2)
	ctrl.expectStreaming(t, primaryConn, codes.Unavailable).Times(1)
	expectUnary(t, primaryConn, codes.OK).Times(1)
	ctrl.expectStreaming(t, primaryConn, codes.OK).Times(1)

	for i := 0; i < 3; i++ {
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	}
	for i := 0; i < 2; i++ {
		s, err := gcpFallback.NewStream(context.Background(), nil, "method")
		if err != nil {
			t.Fatalf("NewStream() error = %v", err)
		}
		s.RecvMsg(nil) // This will trigger the result.
	}

	// Wait for rate check to trigger fallback.
	time.Sleep(20 * time.Millisecond)

	expectUnary(t, fallbackConn, codes.OK)
	if err := gcpFallback.Invoke(context.Background(), "method", nil, nil); err != nil {
		t.Errorf("Invoke() on fallback connection failed: %v", err)
	}
}

func TestGCPFallback_NoFallback_Disabled(t *testing.T) {
	opts := NewGCPFallbackOptions()
	opts.Period = 10 * time.Millisecond
	opts.ErrorRateThreshold = 0.5
	opts.MinFailedCalls = 3
	opts.EnableFallback = false
	_, gcpFallback, primaryConn, _ := setup(t, opts)

	// 3 failures, 2 successes. Rate = 0.6. Conditions met, but fallback is disabled.
	expectUnary(t, primaryConn, codes.Unavailable).Times(3)
	expectUnary(t, primaryConn, codes.OK).Times(2)

	for i := 0; i < 5; i++ {
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	}

	// Wait for rate check.
	time.Sleep(20 * time.Millisecond)

	// Should still use primary.
	expectUnary(t, primaryConn, codes.OK).Times(1)
	gcpFallback.Invoke(context.Background(), "method", nil, nil)
}

func TestGCPFallback_NoFallback_LowRate(t *testing.T) {
	ctrl, gcpFallback, primaryConn, _ := setup(t, nil)

	// 2 failures, 3 successes. Rate = 2/5 = 0.4 < 0.5.
	expectUnary(t, primaryConn, codes.Unavailable).Times(1)
	ctrl.expectStreaming(t, primaryConn, codes.Unavailable).Times(1)
	expectUnary(t, primaryConn, codes.OK).Times(2)
	ctrl.expectStreaming(t, primaryConn, codes.OK).Times(1)

	for i := 0; i < 3; i++ {
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	}
	for i := 0; i < 2; i++ {
		s, err := gcpFallback.NewStream(context.Background(), nil, "method")
		if err != nil {
			t.Fatalf("NewStream() error = %v", err)
		}
		s.RecvMsg(nil) // This will trigger the result.
	}

	// Wait for rate check.
	time.Sleep(20 * time.Millisecond)

	// No fallbck because error rate is lower. Should still use primary.
	expectUnary(t, primaryConn, codes.OK).Times(1)
	gcpFallback.Invoke(context.Background(), "method", nil, nil)
}

func TestGCPFallback_NoFallback_TooFewFailedCalls(t *testing.T) {
	_, gcpFallback, primaryConn, _ := setup(t, nil)

	// 2 failures, 0 successes. Rate = 1.0, but failures = 2 < 3.
	expectUnary(t, primaryConn, codes.Unavailable).Times(2)

	for i := 0; i < 2; i++ {
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	}

	// Wait for rate check.
	time.Sleep(20 * time.Millisecond)

	// Should still use primary.
	expectUnary(t, primaryConn, codes.Unavailable).Times(1)
	gcpFallback.Invoke(context.Background(), "method", nil, nil)
}

func TestGCPFallback_NoFallback_DifferentErrorCode(t *testing.T) {
	ctrl, gcpFallback, primaryConn, _ := setup(t, nil)

	// 6 failures, but with canceled error code.
	expectUnary(t, primaryConn, codes.Canceled).Times(3)
	ctrl.expectStreaming(t, primaryConn, codes.Canceled).Times(3)

	for i := 0; i < 3; i++ {
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	}
	for i := 0; i < 3; i++ {
		s, err := gcpFallback.NewStream(context.Background(), nil, "method")
		if err != nil {
			t.Fatalf("NewStream() error = %v", err)
		}
		s.RecvMsg(nil) // This will trigger the result.
	}

	// Wait for rate check.
	time.Sleep(20 * time.Millisecond)

	// Should still use primary.
	expectUnary(t, primaryConn, codes.OK).Times(1)
	gcpFallback.Invoke(context.Background(), "method", nil, nil)
}
