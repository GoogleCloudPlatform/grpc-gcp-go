package grpcgcp

import (
	"testing"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/resolver"
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
