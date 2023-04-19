package grpcgcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/testing/protocmp"

	pb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
)

var testApiConfig = &pb.ApiConfig{
	ChannelPool: &pb.ChannelPoolConfig{
		MinSize:                          uint32(5),
		MaxSize:                          uint32(10),
		MaxConcurrentStreamsLowWatermark: uint32(50),
		FallbackToReady:                  true,
	},
	Method: []*pb.MethodConfig{
		{
			Name: []string{"method1", "method2"},
			Affinity: &pb.AffinityConfig{
				Command:     pb.AffinityConfig_BOUND,
				AffinityKey: "boundKey",
			},
		},
		{
			Name: []string{"method3"},
			Affinity: &pb.AffinityConfig{
				Command:     pb.AffinityConfig_BIND,
				AffinityKey: "bindKey",
			},
		},
	},
}

var currBalancer *gcpBalancer

// testBuilderWrapper wraps the real gcpBalancerBuilder
// so it can cache the created balancer object
type testBuilderWrapper struct {
	balancer.ConfigParser

	name        string
	realBuilder *gcpBalancerBuilder
}

func (tb *testBuilderWrapper) Build(
	cc balancer.ClientConn,
	opt balancer.BuildOptions,
) balancer.Balancer {
	currBalancer = tb.realBuilder.Build(cc, opt).(*gcpBalancer)
	return currBalancer
}

func (*testBuilderWrapper) Name() string {
	return Name
}

func (tb *testBuilderWrapper) ParseConfig(j json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	return tb.realBuilder.ParseConfig(j)
}

func TestDefaultConfig(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		sc := mocks.NewMockSubConn(mockCtrl)
		sc.EXPECT().Connect().AnyTimes()
		sc.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		return sc, nil
	}).AnyTimes()

	wantCfg := &pb.ApiConfig{
		ChannelPool: &pb.ChannelPoolConfig{
			MinSize:                          defaultMinSize,
			MaxSize:                          defaultMaxSize,
			MaxConcurrentStreamsLowWatermark: defaultMaxStreams,
			FallbackToReady:                  false,
		},
		Method: []*pb.MethodConfig{},
	}

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with no config.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
	})

	if diff := cmp.Diff(wantCfg, b.cfg.ApiConfig, protocmp.Transform()); diff != "" {
		t.Errorf("gcp_balancer config has unexpected difference (-want +got):\n%v", diff)
	}

	b = newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with empty config.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState:  resolver.State{},
		BalancerConfig: &GcpBalancerConfig{},
	})

	if diff := cmp.Diff(wantCfg, b.cfg.ApiConfig, protocmp.Transform()); diff != "" {
		t.Errorf("gcp_balancer config has unexpected difference (-want +got):\n%v", diff)
	}
}

func TestConfig(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		sc := mocks.NewMockSubConn(mockCtrl)
		sc.EXPECT().Connect().AnyTimes()
		sc.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		return sc, nil
	}).AnyTimes()

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with the config provided to Dial.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: testApiConfig,
		},
	})

	if diff := cmp.Diff(testApiConfig, b.cfg.ApiConfig, protocmp.Transform()); diff != "" {
		t.Errorf("gcp_balancer config has unexpected difference (-want +got):\n%v", diff)
	}
}

func TestParseConfig(t *testing.T) {
	json, err := protojson.Marshal(testApiConfig)
	if err != nil {
		t.Fatalf("cannot encode ApiConfig: %v", err)
	}
	cfg, err := newBuilder().(balancer.ConfigParser).ParseConfig(json)
	if err != nil {
		t.Fatalf("ParseConfig returns error: %v, want: nil", err)
	}
	if diff := cmp.Diff(testApiConfig, cfg, protocmp.Transform()); diff != "" {
		t.Errorf("ParseConfig() result has unexpected difference (-want +got):\n%v", diff)
	}
}

func TestParseConfigFromDial(t *testing.T) {
	// Register test builder wrapper.
	balancer.Register(&testBuilderWrapper{
		name:        Name,
		realBuilder: newBuilder().(*gcpBalancerBuilder),
	})

	json, err := protojson.Marshal(testApiConfig)
	if err != nil {
		t.Fatalf("cannot encode ApiConfig: %v", err)
	}

	conn, err := grpc.Dial(
		"localhost:433",
		grpc.WithInsecure(),
		grpc.WithDisableServiceConfig(),
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"loadBalancingConfig": [{"%s":%s}]}`, Name, string(json))),
		grpc.WithUnaryInterceptor(GCPUnaryClientInterceptor),
		grpc.WithStreamInterceptor(GCPStreamClientInterceptor),
	)
	if err != nil {
		t.Errorf("Creation of ClientConn failed due to error: %s", err.Error())
	}
	defer conn.Close()

	if diff := cmp.Diff(testApiConfig, currBalancer.cfg.ApiConfig, protocmp.Transform()); diff != "" {
		t.Errorf("gcp_balancer config has unexpected difference (-want +got):\n%v", diff)
	}
}

func TestCreatesMinSubConns(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockCC := mocks.NewMockClientConn(mockCtrl)
	newSCs := []*mocks.MockSubConn{}
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		newSC := mocks.NewMockSubConn(mockCtrl)
		newSC.EXPECT().Connect().Times(2)
		newSC.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		newSCs = append(newSCs, newSC)
		return newSC, nil
	}).Times(3)

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with the config provided to Dial.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MinSize:                          3,
					MaxSize:                          10,
					MaxConcurrentStreamsLowWatermark: 100,
				},
			},
		},
	})

	if want := 3; len(b.scRefs) != want {
		t.Fatalf("gcpBalancer scRefs length is %v, want %v", len(b.scRefs), want)
	}
	for _, v := range newSCs {
		if _, ok := b.scRefs[v]; !ok {
			t.Fatalf("Created SubConn is not stored in gcpBalancer.scRefs")
		}
	}
}

func TestCreatesUpToMaxSubConns(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockCC := mocks.NewMockClientConn(mockCtrl)
	newSCs := []*mocks.MockSubConn{}
	mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		newSC := mocks.NewMockSubConn(mockCtrl)
		newSC.EXPECT().Connect().AnyTimes()
		newSC.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		newSCs = append(newSCs, newSC)
		return newSC, nil
	}).Times(5)

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with the config provided to Dial.
	minSize := 3
	maxSize := 5
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MinSize:                          uint32(minSize),
					MaxSize:                          uint32(maxSize),
					MaxConcurrentStreamsLowWatermark: 1,
				},
			},
		},
	})

	if want := minSize; len(b.scRefs) != want {
		t.Fatalf("gcpBalancer scRefs length is %v, want %v", len(b.scRefs), want)
	}
	for _, v := range newSCs {
		if _, ok := b.scRefs[v]; !ok {
			t.Fatalf("Created SubConn is not stored in gcpBalancer.scRefs")
		}
		// Simulate ready.
		b.UpdateSubConnState(v, balancer.SubConnState{ConnectivityState: connectivity.Ready})
	}

	// Simulate more than MaxSize calls.
	for i := 0; i < maxSize*2; i++ {
		b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: context.TODO()})
	}

	// Should create only one new subconn because we skip new subconn creation while any is still connecting.
	// This is done to prevent scaling up the pool prematurely.
	if want := minSize + 1; len(b.scRefs) != want {
		t.Fatalf("gcpBalancer scRefs length is %v, want %v", len(b.scRefs), want)
	}

	// Simulate more than MaxSize calls. Now with simulating any new subconn moving to ready state.
	for i := 0; i < maxSize*2; i++ {
		if _, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: context.TODO()}); err != nil {
			// Simulate a new subconn is ready.
			for _, v := range newSCs {
				if b.scStates[v] != connectivity.Ready {
					b.UpdateSubConnState(v, balancer.SubConnState{ConnectivityState: connectivity.Ready})
				}
			}
		}
	}

	if want := maxSize; len(b.scRefs) != want {
		t.Fatalf("gcpBalancer scRefs length is %v, want %v", len(b.scRefs), want)
	}

	for _, v := range newSCs {
		if _, ok := b.scRefs[v]; !ok {
			t.Fatalf("Created SubConn is not stored in gcpBalancer.scRefs")
		}
	}
}

func TestRefreshesSubConnsWhenUnresponsive(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	// A slice to store all SubConns created by gcpBalancer's ClientConn.
	newSCs := []*mocks.MockSubConn{}
	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
	mockCC.EXPECT().RemoveSubConn(gomock.Any()).Times(2)
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		newSC := mocks.NewMockSubConn(mockCtrl)
		newSC.EXPECT().Connect().MinTimes(1)
		newSC.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		newSCs = append(newSCs, newSC)
		return newSC, nil
	}).Times(6)

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with the config provided to Dial.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MinSize:                          3,
					MaxSize:                          10,
					MaxConcurrentStreamsLowWatermark: 50,
					UnresponsiveDetectionMs:          100,
					UnresponsiveCalls:                3,
				},
			},
		},
	})

	// Make subConn 0 ready.
	b.UpdateSubConnState(newSCs[0], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	call := func(expSC balancer.SubConn, errOnDone error) {
		ctx := context.TODO()
		var cancel context.CancelFunc
		if errOnDone == deErr {
			ctx, cancel = context.WithTimeout(ctx, 0)
			defer cancel()
		}
		pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
		if pr.SubConn != expSC || err != nil {
			t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, expSC)
		}
		pr.Done(balancer.DoneInfo{Err: errOnDone})
	}

	// First deadline exceeded call.
	call(newSCs[0], deErr)

	time.Sleep(time.Millisecond * 50)

	// Successful call.
	call(newSCs[0], nil)

	time.Sleep(time.Millisecond * 60)

	// Deadline exceeded calls.
	call(newSCs[0], deErr)
	call(newSCs[0], deErr)

	// Should not trigger new subconn as only ~60ms passed since last response.
	call(newSCs[0], deErr)
	if got, want := len(newSCs), 3; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}

	time.Sleep(time.Millisecond * 50)
	// ~110ms since last response and >= 3 deadline exceeded calls. Should trigger new subconn.
	call(newSCs[0], deErr)
	if got, want := len(newSCs), 4; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}

	// Until the fresh SubConn is ready, the old SubConn should be used.
	dlCtx, cancel := context.WithTimeout(context.TODO(), 0)
	defer cancel()
	pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: dlCtx})
	if want := newSCs[0]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}
	doneOnOld := pr.Done

	// Make replacement subConn 3 ready.
	b.UpdateSubConnState(newSCs[3], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Fresh subConn should be picked.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: context.Background()})
	if want := newSCs[3]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	// A call on the old subconn is finished. Should not panic.
	doneOnOld(balancer.DoneInfo{Err: deErr})

	time.Sleep(time.Millisecond * 110)
	call(newSCs[3], deErr)
	call(newSCs[3], deErr)
	// Should not trigger new subconn as unresponsiveTimeMs should've been doubled when still no response on the fresh subconn.
	call(newSCs[3], deErr)
	if got, want := len(newSCs), 4; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}

	time.Sleep(time.Millisecond * 110)
	// After doubled unresponsiveTimeMs has passed, should trigger new subconn.
	call(newSCs[3], deErr)
	if got, want := len(newSCs), 5; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}

	// Make replacement subConn 4 ready.
	b.UpdateSubConnState(newSCs[4], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Successful call to reset refresh counter.
	call(newSCs[4], nil)

	time.Sleep(time.Millisecond * 110)
	call(newSCs[4], deErr)
	// Only second deadline exceeded call since last response, should not trigger new subconn.
	call(newSCs[4], deErr)
	if got, want := len(newSCs), 5; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}
	// Third call should trigger new subconn.
	call(newSCs[4], deErr)
	if got, want := len(newSCs), 6; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}
}

func TestRefreshingSubConnsDoesNotAffectConnState(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	connState := connectivity.Idle

	// A slice to store all SubConns created by gcpBalancer's ClientConn.
	newSCs := []*mocks.MockSubConn{}
	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().UpdateState(gomock.Any()).Do(func(s balancer.State) {
		connState = s.ConnectivityState
	}).AnyTimes()
	mockCC.EXPECT().RemoveSubConn(gomock.Any()).Times(2)
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		newSC := mocks.NewMockSubConn(mockCtrl)
		newSC.EXPECT().Connect().MinTimes(1)
		newSC.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		newSCs = append(newSCs, newSC)
		return newSC, nil
	}).Times(4)

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with the config provided to Dial.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MinSize:                          2,
					MaxSize:                          2,
					MaxConcurrentStreamsLowWatermark: 50,
					UnresponsiveDetectionMs:          100,
					UnresponsiveCalls:                3,
				},
			},
		},
	})

	// Make subConn 0, 1 ready.
	b.UpdateSubConnState(newSCs[0], balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(newSCs[1], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	calls := make(map[balancer.SubConn][]func(balancer.DoneInfo), 0)

	time.Sleep(time.Millisecond * 110)

	addCall := func() {
		ctx, _ := context.WithTimeout(context.TODO(), 0)
		pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
		if err != nil {
			t.Fatalf("gcpPicker.Pick returns error %v, want: nil", err)
		}
		calls[pr.SubConn] = append(calls[pr.SubConn], pr.Done)
	}

	// Add 3 calls to subConn 0.
	for len(calls[newSCs[0]]) < 3 {
		addCall()
	}

	// Deadline exceeded on all calls for subConn 0.
	for i := 0; i < len(calls[newSCs[0]]); i++ {
		calls[newSCs[0]][i](balancer.DoneInfo{Err: deErr})
	}
	// ~110ms since last response and >= 3 deadline exceeded calls. Should trigger new subconn.
	if got, want := len(newSCs), 3; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}

	// Make new subConn ready.
	b.UpdateSubConnState(newSCs[2], balancer.SubConnState{ConnectivityState: connectivity.Ready})
	// Confirm shutdown of the old subConn.
	b.UpdateSubConnState(newSCs[0], balancer.SubConnState{ConnectivityState: connectivity.Shutdown})

	// Add 3 calls to subConn 1.
	for len(calls[newSCs[1]]) < 3 {
		addCall()
	}

	// Deadline exceeded on all calls for subConn 1.
	for i := 0; i < len(calls[newSCs[1]]); i++ {
		calls[newSCs[1]][i](balancer.DoneInfo{Err: deErr})
	}
	// ~110ms since last response and >= 3 deadline exceeded calls. Should trigger new subconn.
	if got, want := len(newSCs), 4; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}

	// Make new subConn ready.
	b.UpdateSubConnState(newSCs[3], balancer.SubConnState{ConnectivityState: connectivity.Ready})
	// Confirm shutdown of the old subConn.
	b.UpdateSubConnState(newSCs[1], balancer.SubConnState{ConnectivityState: connectivity.Shutdown})

	if connState != connectivity.Ready {
		t.Fatalf("gcpBalancer state changes to %v, want: %v", connState, connectivity.Ready)
	}
}

func TestShutdownWhileRefreshing(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	// A slice to store all SubConns created by gcpBalancer's ClientConn.
	newSCs := []*mocks.MockSubConn{}
	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
	mockCC.EXPECT().RemoveSubConn(gomock.Any()).Times(1)
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		newSC := mocks.NewMockSubConn(mockCtrl)
		newSC.EXPECT().Connect().MinTimes(1)
		newSC.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		newSCs = append(newSCs, newSC)
		return newSC, nil
	}).Times(3)

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with the config provided to Dial.
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MinSize:                          2,
					MaxSize:                          2,
					MaxConcurrentStreamsLowWatermark: 50,
					UnresponsiveDetectionMs:          100,
					UnresponsiveCalls:                3,
				},
			},
		},
	})

	// Make subConn 0, 1 ready.
	b.UpdateSubConnState(newSCs[0], balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(newSCs[1], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	calls := make(map[balancer.SubConn][]func(balancer.DoneInfo), 0)

	time.Sleep(time.Millisecond * 110)

	addCall := func() {
		ctx, _ := context.WithTimeout(context.TODO(), 0)
		pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "", Ctx: ctx})
		if err != nil {
			t.Fatalf("gcpPicker.Pick returns error %v, want: nil", err)
		}
		calls[pr.SubConn] = append(calls[pr.SubConn], pr.Done)
	}

	// Add 3 calls to subConn 0.
	for len(calls[newSCs[0]]) < 3 {
		addCall()
	}

	// Deadline exceeded on all calls for subConn 0.
	for i := 0; i < len(calls[newSCs[0]]); i++ {
		calls[newSCs[0]][i](balancer.DoneInfo{Err: deErr})
	}
	// ~110ms since last response and >= 3 deadline exceeded calls. Should trigger new subconn.
	if got, want := len(newSCs), 3; got != want {
		t.Fatalf("Unexpected number of subConns: %d, want %d", got, want)
	}

	// Before the new subConn is ready we shutdown so all subConns shutdown.
	b.UpdateSubConnState(newSCs[0], balancer.SubConnState{ConnectivityState: connectivity.Shutdown})
	b.UpdateSubConnState(newSCs[1], balancer.SubConnState{ConnectivityState: connectivity.Shutdown})

	// The new subConn becomes briefly ready before shutdown.
	b.UpdateSubConnState(newSCs[2], balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(newSCs[2], balancer.SubConnState{ConnectivityState: connectivity.Shutdown})
}

func TestRoundRobinForBind(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	// A slice to store all SubConns created by gcpBalancer's ClientConn.
	scs := []*mocks.MockSubConn{}
	mockCC := mocks.NewMockClientConn(mockCtrl)
	mockCC.EXPECT().UpdateState(gomock.Any()).AnyTimes()
	mockCC.EXPECT().NewSubConn(gomock.Any(), gomock.Any()).DoAndReturn(func(_, _ interface{}) (*mocks.MockSubConn, error) {
		newSC := mocks.NewMockSubConn(mockCtrl)
		newSC.EXPECT().Connect().MinTimes(1)
		newSC.EXPECT().UpdateAddresses(gomock.Any()).AnyTimes()
		scs = append(scs, newSC)
		return newSC, nil
	}).Times(4)

	b := newBuilder().Build(mockCC, balancer.BuildOptions{}).(*gcpBalancer)
	// Simulate ClientConn calls UpdateClientConnState with the config provided to Dial.
	minSize := 3
	streamsWatermark := 10
	b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{},
		BalancerConfig: &GcpBalancerConfig{
			ApiConfig: &pb.ApiConfig{
				ChannelPool: &pb.ChannelPoolConfig{
					MinSize:                          uint32(minSize),
					MaxSize:                          10,
					MaxConcurrentStreamsLowWatermark: uint32(streamsWatermark),
					BindPickStrategy:                 pb.ChannelPoolConfig_ROUND_ROBIN,
				},
				Method: []*pb.MethodConfig{
					{
						Name: []string{"dummyService/createSession"},
						Affinity: &pb.AffinityConfig{
							Command:     pb.AffinityConfig_BIND,
							AffinityKey: "dummykey",
						},
					},
				},
			},
		},
	})

	if want := minSize; len(b.scRefs) != want {
		t.Fatalf("gcpBalancer scRefs length is %v, want %v", len(b.scRefs), want)
	}

	// Make subConn 1 ready.
	b.UpdateSubConnState(scs[1], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Expect picker will pick the only ready subconn for non-binding call. This call will increment the counter for
	// active streams on subconn 1 which helps us test round-robin for binging calls below.
	pr, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/noAffinity", Ctx: context.TODO()})
	if want := scs[1]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	// Expect 0 subconn because the picker should pick even non-ready subcons for binding calls in a round-robin manner.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[0]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[1]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[2]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	// Cycles to the first subconn.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[0]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[1]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	// Bring other subconns to ready.
	b.UpdateSubConnState(scs[0], balancer.SubConnState{ConnectivityState: connectivity.Ready})
	b.UpdateSubConnState(scs[2], balancer.SubConnState{ConnectivityState: connectivity.Ready})

	// Create more regular calls to reach the limit (watermark*subconns - 6 calls initiated above) to spawn new subconn.
	for i := 0; i < streamsWatermark*minSize-6; i++ {
		_, err := b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/noAffinity", Ctx: context.TODO()})
		if err != nil {
			t.Fatalf("gcpPicker.Pick returns error: %v, want: nil", err)
		}
	}
	// This call triggers new subconn creation.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/noAffinity", Ctx: context.TODO()})
	if wantErr := balancer.ErrNoSubConnAvailable; err != wantErr {
		t.Fatalf("gcpPicker.Pick returns %v, _, %v, want: nil, _, %v", pr.SubConn, err, wantErr)
	}

	// The first binding call shifts to 1 because the divisor for the counter changes from 3 to 4.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[1]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[2]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	// Extends to newly created subconn.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[3]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}

	// Cycles to the first subconn.
	pr, err = b.picker.Pick(balancer.PickInfo{FullMethodName: "dummyService/createSession", Ctx: context.TODO()})
	if want := scs[0]; pr.SubConn != want || err != nil {
		t.Fatalf("gcpPicker.Pick returns %v, %v, want: %v, nil", pr.SubConn, err, want)
	}
}
