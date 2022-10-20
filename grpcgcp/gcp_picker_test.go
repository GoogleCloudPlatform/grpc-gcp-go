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
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
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

func TestGetKeyFromNilMessage(t *testing.T) {
	expectedErr := fmt.Sprintf("cannot get string value from nil message")
	_, err := getAffinityKeyFromMessage("key", nil)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeyFromMessage returns wrong err: %v, want: %v", err, expectedErr)
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

	pr, err := picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
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

	pr, err := picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
	sc := pr.SubConn

	wantErr := balancer.ErrNoSubConnAvailable
	if sc != nil || err != wantErr {
		t.Fatalf("gcpPicker.Pick returns %v, _, %v, want: nil, _, %v", sc, err, wantErr)
	}
	if _, ok := b.scRefs[newSC]; !ok {
		t.Fatalf("Created SubConn is not stored in gcpBalancer.scRefs")
	}
}

func TestCreatesMinSubConns(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockSC := mocks.NewMockSubConn(mockCtrl)
	var scRefs = []*subConnRef{
		{
			subConn:     mockSC,
			affinityCnt: 0,
			streamsCnt:  0,
		},
	}

	mockCC := mocks.NewMockClientConn(mockCtrl)
	newSCs := []*mocks.MockSubConn{}
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		newSC := mocks.NewMockSubConn(mockCtrl)
		newSC.EXPECT().Connect().Times(1)
		newSCs = append(newSCs, newSC)
		return newSC, nil
	}).Times(2)

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
			minConn:   3,
			maxConn:   10,
			maxStream: 100,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	// Concurrent picks.
	wg := sync.WaitGroup{}
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			pr, err := picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
			sc := pr.SubConn

			if sc != mockSC || err != nil {
				t.Errorf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, mockSC)
			}
			wg.Done()
		}()
	}
	wg.Wait()

	if want := 3; len(b.scRefs) != want {
		t.Fatalf("gcpBalancer scRefs length is %v, want %v", len(b.scRefs), want)
	}
	for _, v := range newSCs {
		if _, ok := b.scRefs[v]; !ok {
			t.Fatalf("Created SubConn is not stored in gcpBalancer.scRefs")
		}
	}

	// Subsequent Pick does not create new subconns.
	pr, err := picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
	sc := pr.SubConn
	if sc != mockSC || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, mockSC)
	}
	if want := 3; len(b.scRefs) != want {
		t.Fatalf("gcpBalancer scRefs length is %v, want %v", len(b.scRefs), want)
	}
}

func TestPickMappedSubConn(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockSCnotmapped := mocks.NewMockSubConn(mockCtrl)
	mockSCmapped := mocks.NewMockSubConn(mockCtrl)
	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
	mp := make(map[balancer.SubConn]*subConnRef)
	mp[mockSCnotmapped] = &subConnRef{
		subConn:     mockSCnotmapped,
		affinityCnt: 0,
		streamsCnt:  0,
	}
	mp[mockSCmapped] = &subConnRef{
		subConn:     mockSCmapped,
		affinityCnt: 0,
		streamsCnt:  5,
	}

	// Simulate a pool with two connections.
	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	b.scRefs = mp
	b.scStates[mockSCnotmapped] = connectivity.Idle
	b.scStates[mockSCmapped] = connectivity.Idle

	// Simulate connections moved to the ready state.
	b.UpdateSubConnState(mockSCnotmapped, balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(mockSCmapped, balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Bind the key to the SubConn.
	theKey := "key-for-not-ready"
	b.bindSubConn(theKey, mockSCmapped)

	// Prepare call context with the mapped key.
	ctx := context.Background()
	gcpCtx := &gcpContext{
		poolCfg: &poolConfig{
			maxConn:   10,
			maxStream: 100,
		},
		affinityCfg: &grpc_gcp.AffinityConfig{
			Command:     grpc_gcp.AffinityConfig_BOUND,
			AffinityKey: "key",
		},
		reqMsg: &testMsg{
			Key: theKey,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	// The mapped connection shoud be returned.
	pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
	sc := pr.SubConn

	if sc != mockSCmapped || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, mockSCmapped)
	}

	// Simulate the mapped connection moved to the error state.
	b.UpdateSubConnState(
		mockSCmapped,
		balancer.SubConnState{ConnectivityState: connectivity.TransientFailure},
	)

	// The picker should return ErrNoSubConnAvailable for the same context
	// because the connection mapped to the key is not in the ready state.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
	sc = pr.SubConn

	wantErr := balancer.ErrNoSubConnAvailable
	if sc != nil || err != wantErr {
		t.Fatalf("gcpPicker.Pick returns %v, _, %v, want: nil, _, %v", sc, err, wantErr)
	}
}

func BenchmarkPick(b *testing.B) {
	for _, poolSize := range []int{4, 8, 16, 32, 64} {
		mockCtrl := gomock.NewController(b)

		mockCC := mocks.NewMockClientConn(mockCtrl)
		subconns := []*mocks.MockSubConn{}
		mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
		mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
			newSC := mocks.NewMockSubConn(mockCtrl)
			newSC.EXPECT().Connect().Times(1)
			subconns = append(subconns, newSC)
			return newSC, nil
		}).Times(poolSize)
		bal := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
		bal.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Addresses: []resolver.Address{{Addr: "127.0.0.1"}}}})

		// Make 1st channel ready.
		bal.UpdateSubConnState(subconns[0], balancer.SubConnState{ConnectivityState: connectivity.Ready})

		ctx := context.Background()
		gcpCtx := &gcpContext{
			poolCfg: &poolConfig{
				maxConn:   uint32(poolSize),
				minConn:   uint32(poolSize),
				maxStream: 100,
			},
		}
		ctx = context.WithValue(ctx, gcpKey, gcpCtx)

		// Initiate other channels.
		_, _ = bal.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})

		// Move all subconns to ready state.
		for _, sc := range subconns {
			bal.UpdateSubConnState(sc, balancer.SubConnState{ConnectivityState: connectivity.Ready})
		}

		cwg := sync.WaitGroup{}
		b.Run(fmt.Sprintf("pool_size_%d", poolSize), func(b *testing.B) {
			wg := sync.WaitGroup{}
			n := b.N / poolSize

			// Call Pick concurrently from poolSize parallel jobs.
			for i := 0; i < poolSize; i++ {
				wg.Add(1)
				go func() {
					for j := 0; j < n; j++ {
						pr, _ := bal.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
						cwg.Add(1)
						go func() {
							time.Sleep((5 + time.Duration(rand.Intn(15))) * time.Millisecond)
							pr.Done(balancer.DoneInfo{})
							cwg.Done()
						}()
					}
					wg.Done()
				}()
			}
			wg.Wait()
		})
		// Wait for all Done callbacks.
		cwg.Wait()
		mockCtrl.Finish()
	}
}
