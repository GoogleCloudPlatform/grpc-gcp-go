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

Usage:

1. First, create some_api_config.json file defining API configuration:

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

2. Load configuration to ApiConfig.

	apiConfig, err := grpcgcp.ParseAPIConfig("some_api_config.json")

3. Initialize GCPInterceptor.

	gcpInt := grpcgcp.NewGCPInterceptor(apiConfig)

4. Make ClientConn with specific DialOptions to enable grpc_gcp load balancer.

	conn, err := grpc.Dial(
		target,
		// Following are channel management options:
		grpc.WithBalancerName("grpc_gcp"),
		grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor),
		grpc.WithStreamInterceptor(gcpInt.GCPStreamClientInterceptor),
	)

*/
package grpcgcp // import "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"
