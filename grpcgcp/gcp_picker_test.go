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
	"testing"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
)

type testMsg struct {
	Key         string
	NestedField *nestedField
}

type nestedField struct {
	Key string
}

func (nested *nestedField) getNestedKey() string {
	return nested.Key
}

func TestGetKeyFromMessage(t *testing.T) {
	expectedRes := "test_key"
	msg := &testMsg{
		Key: expectedRes,
		NestedField: &nestedField{
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
	msg := &testMsg{
		Key: "test_key",
		NestedField: &nestedField{
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
	msg := &testMsg{
		Key: "test_key",
		NestedField: &nestedField{
			Key: "test_nested_key",
		},
	}

	locator := "invalidLocator"
	expectedErr := fmt.Sprintf("Cannot get string value from %v", locator)
	_, err := getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "key.invalidLocator"
	expectedErr = fmt.Sprintf("Invalid locator path for %v", locator)
	_, err = getAffinityKeyFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "nestedField.key.invalidLocator"
	expectedErr = fmt.Sprintf("Invalid locator path for %v", locator)
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
		{
			subConn:     mocks.NewMockSubConn(mockCtrl),
			affinityCnt: 0,
			streamsCnt:  1,
		},
		{
			subConn:     okSC,
			affinityCnt: 0,
			streamsCnt:  0,
		},
		{
			subConn:     mocks.NewMockSubConn(mockCtrl),
			affinityCnt: 0,
			streamsCnt:  3,
		},
		{
			subConn:     mocks.NewMockSubConn(mockCtrl),
			affinityCnt: 0,
			streamsCnt:  5,
		},
	}

	picker := newGCPPicker(scRefs, &gcpBalancer{})

	ctx := context.Background()
	gcpCtx := &gcpContext{
		poolCfg: &poolConfig{
			maxConn:   10,
			maxStream: 100,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	pr, err := picker.Pick(balancer.PickInfo{"", ctx})
	sc := pr.SubConn

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
		{
			subConn:     mockSC,
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
		cc:       mockCC,
		scRefs:   mp,
		scStates: make(map[balancer.SubConn]connectivity.State),
	}

	picker := newGCPPicker(scRefs, b)

	ctx := context.Background()
	gcpCtx := &gcpContext{
		poolCfg: &poolConfig{
			maxConn:   10,
			maxStream: 100,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	pr, err := picker.Pick(balancer.PickInfo{"", ctx})
	sc := pr.SubConn

	wantErr := balancer.ErrNoSubConnAvailable
	if sc != nil || err != wantErr {
		t.Fatalf("gcpPicker.Pick returns %v, _, %v, want: nil, _, %v", sc, err, wantErr)
	}
	if _, ok := b.scRefs[newSC]; !ok {
		t.Fatalf("Created SubConn is not stored in gcpBalancer.scRefs")
	}
}
