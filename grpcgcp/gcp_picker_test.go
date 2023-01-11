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

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"

	pb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
)

type testMsg struct {
	Key            string
	NestedField    *nestedField
	RepeatedField  []*nestedField
	RepeatedString []string
	RepeatedInt    []int
}

type nestedField struct {
	Key            string
	RepeatedString []string
}

func TestGetKeyFromMessage(t *testing.T) {
	expectedRes := []string{"test_key"}
	msg := &testMsg{
		Key: "test_key",
		NestedField: &nestedField{
			Key: "test_nested_key",
		},
	}
	locator := "key"
	res, err := getAffinityKeysFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeysFromMessage failed: %v", err)
	}
	if diff := cmp.Diff(expectedRes, res); diff != "" {
		t.Fatalf("getAffinityKeysFromMessage returns unexpected diff (-want, +got):\n%s", diff)
	}
}

func TestGetNestedKeyFromMessage(t *testing.T) {
	expectedRes := []string{"test_nested_key"}
	msg := &testMsg{
		Key: "test_key",
		NestedField: &nestedField{
			Key: "test_nested_key",
		},
	}
	locator := "nestedField.key"
	res, err := getAffinityKeysFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeysFromMessage failed: %v", err)
	}
	if diff := cmp.Diff(expectedRes, res); diff != "" {
		t.Fatalf("getAffinityKeysFromMessage returns unexpected diff (-want, +got):\n%s", diff)
	}
}

func TestGetListOfKeysFromRepeatedStruct(t *testing.T) {
	msg := &testMsg{
		Key: "test_key",
		RepeatedField: []*nestedField{
			{
				Key: "key1",
			},
			{
				Key: "key2",
			},
		},
	}

	expected := []string{"key1", "key2"}
	locator := "repeatedField.key"
	res, err := getAffinityKeysFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeysFromMessage failed: %v", err)
	}
	if diff := cmp.Diff(expected, res); diff != "" {
		t.Fatalf("getAffinityKeysFromMessage returns unexpected diff (-want, +got):\n%s", diff)
	}
}

func TestGetListOfKeysFromRepeatedString(t *testing.T) {
	msg := &testMsg{
		Key: "test_key",
		RepeatedString: []string{
			"key1",
			"key2",
		},
	}

	expected := []string{"key1", "key2"}
	locator := "repeatedString"
	res, err := getAffinityKeysFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeysFromMessage failed: %v", err)
	}
	if diff := cmp.Diff(expected, res); diff != "" {
		t.Fatalf("getAffinityKeysFromMessage returns unexpected diff (-want, +got):\n%s", diff)
	}
}

func TestGetListOfKeysFromRepeatedStringInRepeatedStruct(t *testing.T) {
	msg := &testMsg{
		Key: "test_key",
		RepeatedField: []*nestedField{
			{
				RepeatedString: []string{"key1", "key2"},
			},
			{
				RepeatedString: []string{"key3", "key4"},
			},
		},
	}

	expected := []string{"key1", "key2", "key3", "key4"}
	locator := "repeatedField.repeatedString"
	res, err := getAffinityKeysFromMessage(locator, msg)
	if err != nil {
		t.Fatalf("getAffinityKeysFromMessage failed: %v", err)
	}
	if diff := cmp.Diff(expected, res); diff != "" {
		t.Fatalf("getAffinityKeysFromMessage returns unexpected diff (-want, +got):\n%s", diff)
	}
}

func TestGetListOfKeysFromRepeatedInt(t *testing.T) {
	msg := &testMsg{
		Key:         "test_key",
		RepeatedInt: []int{42, 43},
	}

	locator := "repeatedInt"
	expectedErr := fmt.Sprintf("cannot get string value from %q which is \"int\"", locator)
	_, err := getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}
}

func TestGetKeyFromNilMessage(t *testing.T) {
	expectedErr := "path \"key\" traversal error: cannot lookup field \"key\" (index 0 in the path) in a \"invalid\" value"
	_, err := getAffinityKeysFromMessage("key", nil)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}
}

func TestGetKeyFromNonStructMessage(t *testing.T) {
	expectedErr := "path \"key\" traversal error: cannot lookup field \"key\" (index 0 in the path) in a \"int\" value"
	_, err := getAffinityKeysFromMessage("key", 42)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}
}

func TestInvalidKeyLocator(t *testing.T) {
	msg := &testMsg{
		Key: "test_key",
		NestedField: &nestedField{
			Key: "test_nested_key",
		},
		RepeatedField: []*nestedField{
			{
				Key:            "test_nested_repeated_key1",
				RepeatedString: []string{"test_nested_repeated_key2"},
			},
		},
	}

	locator := "invalidLocator"
	expectedErr := fmt.Sprintf("cannot get string value from %q which is \"invalid\"", locator)
	_, err := getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "nestedField.invalidLocator"
	expectedErr = fmt.Sprintf("cannot get string value from %q which is \"invalid\"", locator)
	_, err = getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "repeatedField.invalidLocator"
	expectedErr = fmt.Sprintf("cannot get string value from %q which is \"invalid\"", locator)
	_, err = getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "repeatedField.repeatedString.invalidLocator"
	expectedErr = fmt.Sprintf("path %q traversal error: cannot lookup field \"invalidLocator\" (index 2 in the path) in a \"string\" value", locator)
	_, err = getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "key.invalidLocator"
	expectedErr = fmt.Sprintf("path %q traversal error: cannot lookup field \"invalidLocator\" (index 1 in the path) in a \"string\" value", locator)
	_, err = getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "nestedField.key.invalidLocator"
	expectedErr = fmt.Sprintf("path %q traversal error: cannot lookup field \"invalidLocator\" (index 2 in the path) in a \"string\" value", locator)
	_, err = getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
	}

	locator = "repeatedField.key.invalidLocator"
	expectedErr = fmt.Sprintf("path %q traversal error: cannot lookup field \"invalidLocator\" (index 2 in the path) in a \"string\" value", locator)
	_, err = getAffinityKeysFromMessage(locator, msg)
	if err == nil || err.Error() != expectedErr {
		t.Fatalf("getAffinityKeysFromMessage returns wrong err: %v, want: %v", err, expectedErr)
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

	picker := newGCPPicker(scRefs, &gcpBalancer{cfg: &GcpBalancerConfig{
		ApiConfig: &pb.ApiConfig{
			ChannelPool: &pb.ChannelPoolConfig{
				MaxSize:                          10,
				MaxConcurrentStreamsLowWatermark: 100,
			},
		},
	}})

	ctx := context.Background()

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
		cfg: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MaxSize:                          10,
					MaxConcurrentStreamsLowWatermark: 100,
				},
			},
		},
	}

	picker := newGCPPicker(scRefs, b)

	ctx := context.Background()

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

func TestBindSubConn(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	scBusy := mocks.NewMockSubConn(mockCtrl)
	scIdle := mocks.NewMockSubConn(mockCtrl)
	scBusy.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
	scIdle.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
	scBusy.EXPECT().Connect().AnyTimes()
	scIdle.EXPECT().Connect().AnyTimes()
	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
	mp := make(map[balancer.SubConn]*subConnRef)
	mp[scBusy] = &subConnRef{
		subConn:     scBusy,
		affinityCnt: 0,
		streamsCnt:  5,
	}
	mp[scIdle] = &subConnRef{
		subConn:     scIdle,
		affinityCnt: 0,
		streamsCnt:  0,
	}

	testMethod := "testBindMethod"
	gcpcfg := &GcpBalancerConfig{
		ApiConfig: &pb.ApiConfig{
			ChannelPool: &pb.ChannelPoolConfig{
				MaxSize:                          2,
				MaxConcurrentStreamsLowWatermark: 100,
			},
			Method: []*pb.MethodConfig{
				{
					Name: []string{testMethod},
					Affinity: &pb.AffinityConfig{
						Command:     pb.AffinityConfig_BIND,
						AffinityKey: "key",
					},
				},
			},
		},
	}

	// Simulate a pool with two connections.
	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	b.scRefs = mp
	b.scStates[scBusy] = connectivity.Idle
	b.scStates[scIdle] = connectivity.Idle
	// Simulate resolver.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: b.addrs,
		},
		BalancerConfig: gcpcfg,
	})

	// Simulate connections moved to the ready state.
	b.UpdateSubConnState(scBusy, balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(scIdle, balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Prepare a bind call context.
	ctx := context.Background()
	gcpCtx := &gcpContext{
		reqMsg: &testMsg{},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	// Idle subconn shoud be returned.
	pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
	sc := pr.SubConn
	if sc != scIdle || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, scIdle)
	}

	testKey := "test_key"
	// Simulate bind call completion.
	gcpCtx.replyMsg = &testMsg{
		Key: testKey,
	}
	pr.Done(balancer.DoneInfo{})

	// Make sure the key is mapped to the subconn.
	if mappedSc, ok := b.affinityMap[testKey]; !ok || mappedSc != scIdle {
		t.Fatalf("b.affinityMap[testKey] returned: %v, %v, want: %v, %v", mappedSc, ok, scIdle, true)
	}
}

func TestPickMappedSubConn(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockSCnotmapped := mocks.NewMockSubConn(mockCtrl)
	mockSCmapped := mocks.NewMockSubConn(mockCtrl)
	mockSCnotmapped.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
	mockSCmapped.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
	mockSCnotmapped.EXPECT().Connect().AnyTimes()
	mockSCmapped.EXPECT().Connect().AnyTimes()
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

	testMethod := "testMethod"
	gcpcfg := &GcpBalancerConfig{
		ApiConfig: &pb.ApiConfig{
			ChannelPool: &pb.ChannelPoolConfig{
				MaxSize:                          2,
				MaxConcurrentStreamsLowWatermark: 100,
			},
			Method: []*pb.MethodConfig{
				{
					Name: []string{testMethod},
					Affinity: &pb.AffinityConfig{
						Command:     pb.AffinityConfig_BOUND,
						AffinityKey: "key",
					},
				},
			},
		},
	}

	// Simulate a pool with two connections.
	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	b.scRefs = mp
	b.scStates[mockSCnotmapped] = connectivity.Idle
	b.scStates[mockSCmapped] = connectivity.Idle
	// Simulate resolver.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: b.addrs,
		},
		BalancerConfig: gcpcfg,
	})

	// Simulate connections moved to the ready state.
	b.UpdateSubConnState(mockSCnotmapped, balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(mockSCmapped, balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Bind the key to the SubConn.
	theKey := "key-for-not-ready"
	b.bindSubConn(theKey, mockSCmapped)

	// Prepare call context with the mapped key.
	ctx := context.Background()
	gcpCtx := &gcpContext{
		reqMsg: &testMsg{
			Key: theKey,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	// The mapped connection shoud be returned.
	pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
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
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
	sc = pr.SubConn

	wantErr := balancer.ErrNoSubConnAvailable
	if sc != nil || err != wantErr {
		t.Fatalf("gcpPicker.Pick returns %v, _, %v, want: nil, _, %v", sc, err, wantErr)
	}
}

func TestPickSubConnWithFallback(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	scs := []*mocks.MockSubConn{}
	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		sc := mocks.NewMockSubConn(mockCtrl)
		sc.EXPECT().Connect().AnyTimes()
		sc.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		scs = append(scs, sc)
		return sc, nil
	}).Times(3)

	// Create new pool.
	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate resolver.
	testMethod := "testMethod"
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: b.addrs,
		},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MinSize:                          3,
					MaxSize:                          3,
					MaxConcurrentStreamsLowWatermark: 100,
					FallbackToReady:                  true,
				},
				Method: []*pb.MethodConfig{
					{
						Name: []string{testMethod},
						Affinity: &pb.AffinityConfig{
							Command:     pb.AffinityConfig_BOUND,
							AffinityKey: "key",
						},
					},
				},
			},
		},
	})
	// Move first two subconn to ready.
	b.UpdateSubConnState(scs[0], balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(scs[1], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Bind the key to the subconn 2, which is not ready.
	theKey := "key-for-not-ready"
	b.bindSubConn(theKey, scs[2])

	ctx := context.Background()
	// Prepare call context with the mapped key.
	gcpCtx := &gcpContext{
		reqMsg: &testMsg{
			Key: theKey,
		},
	}
	ctx = context.WithValue(ctx, gcpKey, gcpCtx)

	// Increase active streams on subconn 0 so that the pick below will be forced to subconn 1.
	b.scRefs[scs[0]].streamsIncr()
	// Despite subconn 2 is mapped to the key, subconn 1 shoud be returned as a fallback
	// because it has less active streams than subconn 0.
	pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
	sc := pr.SubConn

	if sc != scs[1] || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, scs[1])
	}

	// Should stick to subconn 1 even when it has more active streams.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
	sc = pr.SubConn

	if sc != scs[1] || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, scs[1])
	}
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
	sc = pr.SubConn

	if sc != scs[1] || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, scs[1])
	}

	// Simulate subconn 1 moved to the error state.
	b.UpdateSubConnState(scs[1], balancer.SubConnState{ConnectivityState: connectivity.TransientFailure})

	// Subconn 0 shoud be returned, because now subconn 1 (temporary fallback) is also not ready.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
	sc = pr.SubConn

	if sc != scs[0] || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, scs[0])
	}

	// Simulate subconn 2 recovered.
	b.UpdateSubConnState(scs[2], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// As originally mapped subconn 2 is recovered, it shoud be returned.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: testMethod, Ctx: ctx})
	sc = pr.SubConn

	if sc != scs[2] || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", sc, err, scs[2])
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
			newSC.EXPECT().Connect().AnyTimes()
			newSC.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
			subconns = append(subconns, newSC)
			return newSC, nil
		}).Times(poolSize)
		bal := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
		bal.UpdateClientConnState(
			balancer.ClientConnState{
				ResolverState: resolver.State{
					Addresses: []resolver.Address{{Addr: "127.0.0.1"}},
				},
				BalancerConfig: &GcpBalancerConfig{
					ApiConfig: &pb.ApiConfig{
						ChannelPool: &pb.ChannelPoolConfig{
							MinSize:                          uint32(poolSize),
							MaxSize:                          uint32(poolSize),
							MaxConcurrentStreamsLowWatermark: 100,
							FallbackToReady:                  true,
						},
					},
				},
			},
		)

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
						pr, _ := bal.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: context.Background()})
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
