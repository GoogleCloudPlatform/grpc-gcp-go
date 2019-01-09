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
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"fmt"
	"testing"
	"context"
	"github.com/golang/mock/gomock"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
)

type TestMsg struct {
	Key string
	NestedField *NestedField
}

type NestedField struct {
	Key string
}

func (nested *NestedField) getNestedKey() string {
	return nested.Key
}

func TestGetKeyFromMessage(t *testing.T) {
	expectedRes := "test_key"
	msg := &TestMsg{
		Key: expectedRes,
		NestedField: &NestedField{
			Key: "test_nested_key",
		},
	}
	locator := "key"
	res, err := getAffinityKeyFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeyFromMessage failed: %v", err)
	}
	if res != expectedRes {
		t.Fatalf("getAffinityKeyFromMessage returns wrong key: %v, want: %v", res, expectedRes)
	}
}

func TestGetNestedKeyFromMessage(t *testing.T) {
	expectedRes := "test_nested_key"
	msg := &TestMsg{
		Key: "test_key",
		NestedField: &NestedField{
			Key: expectedRes,
		},
	}
	locator := "nestedField.key"
	res, err := getAffinityKeyFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeyFromMessage failed: %v", err)
	}
	if res != expectedRes {
		t.Fatalf("getAffinityKeyFromMessage returns wrong key: %v, want: %v", res, expectedRes)
	}
}

func TestInvalidKeyLocator(t *testing.T) {
	msg := &TestMsg{
		Key: "test_key",
		NestedField: &NestedField{
			Key: "test_nested_key",
		},
	}

	locator := "invalidLocator"
	expectedErr := fmt.Sprintf("cannot get valid affinity key from locator: %v", locator)
	_, err := getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "key.invalidLocator"
	expectedErr = fmt.Sprintf("cannot get valid affinity key from locator: %v", locator)
	_, err = getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "nestedField.key.invalidLocator"
	expectedErr = fmt.Sprintf("cannot get valid affinity key from locator: %v", locator)
	_, err = getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}
}

func TestPickSubConnWithLeastStreams(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	okSC := mocks.NewMockSubConn(mockCtrl)
	var scRefs = []*subConnRef{
		&subConnRef{
			subConn: mocks.NewMockSubConn(mockCtrl),
			scState: connectivity.Ready,
			affinityCnt: 0,
			streamsCnt:  1,
		},
		&subConnRef{
			subConn: okSC,
			scState: connectivity.Ready,
			affinityCnt: 0,
			streamsCnt:  0,
		},
		&subConnRef{
			subConn: mocks.NewMockSubConn(mockCtrl),
			scState: connectivity.Ready,
			affinityCnt: 0,
			streamsCnt:  3,
		},
		&subConnRef{
			subConn: mocks.NewMockSubConn(mockCtrl),
			scState: connectivity.Ready,
			affinityCnt: 0,
			streamsCnt:  5,
		},
	}

	picker := newGCPPicker(scRefs, &gcpBalancer{})

	ctx := context.Background()
	gcpCtx := &gcpContext{
		channelPoolCfg: &grpc_gcp.ChannelPoolConfig{
			MaxSize: 10,
			MaxConcurrentStreamsLowWatermark: 100,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	sc, _, err := picker.Pick(ctx, balancer.PickOptions{})

	if err != nil {
		t.Fatalf("gcpPicker.Pick returns err: %v", err)
	}
	if sc != okSC {
		t.Fatalf("gcpPicker.Pick returns wrong SubConn: %v, want: %v", sc, okSC)
	}
}

func TestPickNewSubConn(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockSC := mocks.NewMockSubConn(mockCtrl)
	var scRefs = []*subConnRef{
		&subConnRef{
			subConn: mockSC,
			scState: connectivity.Ready,
			affinityCnt: 0,
			streamsCnt:  100,
		},
	}

	mockCC := mocks.NewMockClientConn(mockCtrl)
	newSC := mocks.NewMockSubConn(mockCtrl)
	newSC.EXPECT().Connect().Times(1)
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).Return(newSC, nil).Times(1)

	mp := make(map[balancer.SubConn]*subConnRef)
	mp[mockSC] = scRefs[0]
	b := &gcpBalancer{
		cc: mockCC,
		scRefs: mp,
	}

	picker := newGCPPicker(scRefs, b)

	ctx := context.Background()
	gcpCtx := &gcpContext{
		channelPoolCfg: &grpc_gcp.ChannelPoolConfig{
			MaxSize: 10,
			MaxConcurrentStreamsLowWatermark: 100,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	sc, _, err := picker.Pick(ctx, balancer.PickOptions{})

	wantErr := balancer.ErrNoSubConnAvailable
	if sc != nil || err != wantErr {
		t.Fatalf("gcpPicker.Pick returns %v, _, %v, want: nil, _, %v", sc, err, wantErr)
	}
	if _, ok := b.scRefs[newSC]; !ok {
		t.Fatalf("Created SubConn is not stored in gcpBalancer.scRefs")
	}
}