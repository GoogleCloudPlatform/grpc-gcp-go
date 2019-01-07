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

Let's use Spanner API as an example.

1. First, Create a json file defining API configuration, with ChannelPoolConfig and MethodConfig.

	{
		"channelPool": {
			"maxSize": 10,
			"maxConcurrentStreamsLowWatermark": 1
		},
		"method": [
			{
				"name": [ "/google.spanner.v1.Spanner/CreateSession" ],
				"affinity": {
					"command": "BIND",
					"affinityKey": "name"
				}
			},
			{
				"name": [ "/google.spanner.v1.Spanner/GetSession" ],
				"affinity": {
					"command": "BOUND",
					"affinityKey": "name"
				}
			},
			{
				"name": [ "/google.spanner.v1.Spanner/DeleteSession" ],
				"affinity": {
					"command": "UNBIND",
					"affinityKey": "name"
				}
			}
		]
	}

2. Load configuration to ApiConfig.

	apiConfig, err := grpcgcp.ParseAPIConfig("some_config.json")

3. Initialize GCPInterceptor.

	gcpInt := grpcgcp.NewGCPInterceptor(apiConfig)

4. Make ClientConn with specific DialOptions.

	conn, err := grpc.Dial(
		target,
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
		grpc.WithPerRPCCredentials(perRPC),
		
		// Following are channel management options:
		grpc.WithBalancerName("grpc_gcp"),
		grpc.WithUnaryInterceptor(gcpInt.GCPUnaryClientInterceptor),
		grpc.WithStreamInterceptor(gcpInt.GCPStreamClientInterceptor),
	)

*/
package grpcgcp // import "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp"