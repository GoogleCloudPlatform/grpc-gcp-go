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
	"testing"

	configpb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	"google.golang.org/grpc/balancer"
)

func TestInitApiConfig(t *testing.T) {
	expectedSize := uint32(10)
	expectedStreams := uint32(10)
	apiConfig := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MaxSize:                          expectedSize,
			MaxConcurrentStreamsLowWatermark: expectedStreams,
		},
	}
	gcpInt := NewGCPInterceptor(apiConfig)
	if gcpInt.poolCfg.maxConn != expectedSize {
		t.Errorf("poolCfg has incorrect maxConn: %v, want: %v", gcpInt.poolCfg.maxConn, expectedSize)
	}
	if gcpInt.poolCfg.maxStream != expectedStreams {
		t.Errorf("poolCfg has incorrect maxStream: %v, want: %v", gcpInt.poolCfg.maxStream, expectedStreams)
	}
	if len(gcpInt.methodToAffinity) != 0 {
		t.Errorf("methodToAffinity has incorrect size: %v, want: %v", len(gcpInt.methodToAffinity), 0)
	}
}

func TestDefaultApiConfig(t *testing.T) {
	defaultSize := uint32(0)
	defaultStreams := uint32(100)
	apiConfig := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{},
	}
	gcpInt := NewGCPInterceptor(apiConfig)
	if gcpInt.poolCfg.maxConn != defaultSize {
		t.Errorf("poolCfg has incorrect maxConn: %v, want: %v", gcpInt.poolCfg.maxConn, defaultSize)
	}
	if gcpInt.poolCfg.maxStream != defaultStreams {
		t.Errorf("poolCfg has incorrect maxStream: %v, want: %v", gcpInt.poolCfg.maxStream, defaultStreams)
	}

	apiConfig = &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MaxConcurrentStreamsLowWatermark: 0,
		},
	}
	gcpInt = NewGCPInterceptor(apiConfig)
	if gcpInt.poolCfg.maxConn != defaultSize {
		t.Errorf("poolCfg has incorrect maxConn: %v, want: %v", gcpInt.poolCfg.maxConn, defaultSize)
	}
	if gcpInt.poolCfg.maxStream != defaultStreams {
		t.Errorf("poolCfg has incorrect maxStream: %v, want: %v", gcpInt.poolCfg.maxStream, defaultStreams)
	}

	apiConfig = &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MaxConcurrentStreamsLowWatermark: 200,
		},
	}
	gcpInt = NewGCPInterceptor(apiConfig)
	if gcpInt.poolCfg.maxConn != defaultSize {
		t.Errorf("poolCfg has incorrect maxConn: %v, want: %v", gcpInt.poolCfg.maxConn, defaultSize)
	}
	if gcpInt.poolCfg.maxStream != defaultStreams {
		t.Errorf("poolCfg has incorrect maxStream: %v, want: %v", gcpInt.poolCfg.maxStream, defaultStreams)
	}
}

func TestParseJsonApiConfig(t *testing.T) {
	expectedSize := uint32(10)
	expectedStreams := uint32(10)
	apiConfig, err := ParseAPIConfig("test_config.json")
	if err != nil {
		t.Fatalf("Failed to parse api config file: %v", err)
	}

	// Register test builder wrapper
	balancer.Register(&testBuilderWrapper{
		name:        Name,
		realBuilder: &gcpBalancerBuilder{name: Name},
	})

	gcpInt := NewGCPInterceptor(apiConfig)

	if gcpInt.poolCfg.maxConn != expectedSize {
		t.Errorf("poolCfg has incorrect maxConn: %v, want: %v", gcpInt.poolCfg.maxConn, expectedSize)
	}
	if gcpInt.poolCfg.maxStream != expectedStreams {
		t.Errorf("poolCfg has incorrect maxStream: %v, want: %v", gcpInt.poolCfg.maxStream, expectedStreams)
	}

	if gcpInt.methodToAffinity == nil {
		t.Fatalf("gcpInt.methodToAffinity should not be nil")
	}

	expectedMethods := 3
	if len(gcpInt.methodToAffinity) != expectedMethods {
		t.Errorf("methodToAffinity has incorrect size: %v, want: %v", len(gcpInt.methodToAffinity), expectedMethods)
	}

	methodName := "method1"
	affCfg, ok := gcpInt.methodToAffinity[methodName]
	if !ok {
		t.Fatalf("gcpInt.methodToAffinity should contain key: %v", methodName)
	}
	if affCfg.GetCommand() != configpb.AffinityConfig_BIND {
		t.Errorf("affinity config has incorrect command: %v, want: %v", affCfg.GetCommand(), configpb.AffinityConfig_BIND)
	}

	methodName = "method2"
	affCfg, ok = gcpInt.methodToAffinity[methodName]
	if !ok {
		t.Fatalf("gcpInt.methodToAffinity should contain key: %v", methodName)
	}
	if affCfg.GetCommand() != configpb.AffinityConfig_BOUND {
		t.Errorf("affinity config has incorrect command: %v, want: %v", affCfg.GetCommand(), configpb.AffinityConfig_BOUND)
	}

	methodName = "method3"
	affCfg, ok = gcpInt.methodToAffinity[methodName]
	if !ok {
		t.Fatalf("gcpInt.methodToAffinity should contain key: %v", methodName)
	}
	if affCfg.GetCommand() != configpb.AffinityConfig_UNBIND {
		t.Errorf("affinity config has incorrect command: %v, want: %v", affCfg.GetCommand(), configpb.AffinityConfig_UNBIND)
	}
}
