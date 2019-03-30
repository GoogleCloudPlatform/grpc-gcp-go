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

/*
Package grpcgcp provides grpc supports for Google Cloud APIs.
For now it provides connection management with affinity support.

Note: "channel" is analagous to "connection" in our context.

Usage:

1. First, initialize the api configuration. There are two ways:

	1a. Create a json file defining the configuration and use `ParseAPIConfig()`
		to convert the json file to ApiConfig object.

		// Create some_api_config.json
		{
			"channelPool": {
				"maxSize": 10,
				"maxConcurrentStreamsLowWatermark": 1
			},
			"method": [
				{
					"name": [ "/some.api.v1/Method1" ],
					"affinity": {
						"command": "BIND",
						"affinityKey": "key1"
					}
				},
				{
					"name": [ "/some.api.v1/Method2" ],
					"affinity": {
						"command": "BOUND",
						"affinityKey": "key2"
					}
				},
				{
					"name": [ "/some.api.v1/Method3" ],
					"affinity": {
						"command": "UNBIND",
						"affinityKey": "key3"
					}
				}
			]
		}

		// Then convert json file to apiConfig object:

		apiConfig, err := grpcgcp.ParseAPIConfig("some_api_config.json")

	1b. Create apiConfig directly.

		// import (
		// 	configpb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
		// )

		apiConfig := &configpb.ApiConfig{
		ChannelPool: &configpb.ChannelPoolConfig{
			MaxSize:                          10,
			MaxConcurrentStreamsLowWatermark: 1,
		},
		Method: []*configpb.MethodConfig{
			&configpb.MethodConfig{
				Name: []string{"/some.api.v1/Method1"},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_BIND,
					AffinityKey: "key1",
				},
			},
			&configpb.MethodConfig{
				Name: []string{"/some.api.v1/Method2"},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_BOUND,
					AffinityKey: "key2",
				},
			},
			&configpb.MethodConfig{
				Name: []string{"/some.api.v1/Method3"},
				Affinity: &configpb.AffinityConfig{
					Command:     configpb.AffinityConfig_UNBIND,
					AffinityKey: "key3",
				},
			},
		},

2. Initialize GCPInterceptor.

	gcpInt := grpcgcp.NewGCPInterceptor(apiConfig)

3. Make ClientConn with specific DialOptions to enable grpc_gcp load balancer.

	conn, err := grpc.Dial(
		target,
		// Register and specify the grpc-gcp load balancer.
		grpc.WithBalancerName("grpc_gcp"),
		grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor),
		grpc.WithStreamInterceptor(gcpInt.GCPStreamClientInterceptor),
	)

*/
package grpcgcp // import "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
