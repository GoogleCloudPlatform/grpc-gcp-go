/*
 *
 * Copyright 2023 gRPC authors.
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
	"sync"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/multiendpoint"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
)

var (
	// To be redefined in tests.
	grpcDial = func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return grpc.Dial(target, opts...)
	}
)

var log = grpclog.Component("GCPMultiEndpoint")

type contextMEKey int

var meKey contextMEKey

// NewMEContext returns a new Context that carries Multiendpoint name meName.
func NewMEContext(ctx context.Context, meName string) context.Context {
	return context.WithValue(ctx, meKey, meName)
}

// FromMEContext returns the MultiEndpoint name stored in ctx, if any.
func FromMEContext(ctx context.Context) (string, bool) {
	meName, ok := ctx.Value(meKey).(string)
	return meName, ok
}

// GCPMultiEndpoint holds the state of MultiEndpoints-enabled gRPC client connection.
type GCPMultiEndpoint struct {
	sync.Mutex

	defaultName string
	mes         map[string]multiendpoint.MultiEndpoint
	pools       map[string]*monitoredConn
	opts        []grpc.DialOption
	gcpConfig   *pb.ApiConfig

	grpc.ClientConnInterface
}

// Make sure GcpMultiEndpoint implements grpc.ClientConnInterface.
var _ grpc.ClientConnInterface = &GCPMultiEndpoint{}

func (gme *GCPMultiEndpoint) Invoke(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
	return gme.pickConn(ctx).Invoke(ctx, method, args, reply, opts...)
}

func (gme *GCPMultiEndpoint) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return gme.pickConn(ctx).NewStream(ctx, desc, method, opts...)
}

func (gme *GCPMultiEndpoint) pickConn(ctx context.Context) *grpc.ClientConn {
	name, ok := FromMEContext(ctx)
	me, ook := gme.mes[name]
	if !ok || !ook {
		me = gme.mes[gme.defaultName]
	}
	return gme.pools[me.Current()].conn
}

func (gme *GCPMultiEndpoint) Close() error {
	var errs multiError
	for _, mc := range gme.pools {
		mc.stopMonitoring()
		if err := mc.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs.Combine()
}

// GCPMultiEndpointOptions holds options to construct a MultiEndpoints-enabled gRPC client
// connection.
type GCPMultiEndpointOptions struct {
	// Regular gRPC-GCP configuration to be applied to every endpoint.
	GRPCgcpConfig *pb.ApiConfig
	// Map of MultiEndpoints where key is the MultiEndpoint name.
	MultiEndpoints map[string]*multiendpoint.MultiEndpointOptions
	// Name of the default MultiEndpoint.
	Default string
}

// NewGcpMultiEndpoint creates new GCPMultiEndpoint -- MultiEndpoints-enabled gRPC client
// connection.
// GCPMultiEndpoint implements `grpc.ClientConnInterface` and can be used
// as a `grpc.ClientConn` when creating gRPC clients.
func NewGcpMultiEndpoint(meOpts *GCPMultiEndpointOptions, opts ...grpc.DialOption) (*GCPMultiEndpoint, error) {
	// Read config, create multiendpoints and pools.
	o, err := makeOpts(meOpts, opts)
	if err != nil {
		return nil, err
	}
	gme := &GCPMultiEndpoint{
		mes:         make(map[string]multiendpoint.MultiEndpoint),
		pools:       make(map[string]*monitoredConn),
		defaultName: meOpts.Default,
		opts:        o,
		gcpConfig:   meOpts.GRPCgcpConfig,
	}
	if err := gme.UpdateMultiEndpoints(meOpts); err != nil {
		return nil, err
	}
	return gme, nil
}

func makeOpts(meOpts *GCPMultiEndpointOptions, opts []grpc.DialOption) ([]grpc.DialOption, error) {
	grpcGCPjsonConfig, err := protojson.Marshal(meOpts.GRPCgcpConfig)
	if err != nil {
		return nil, err
	}
	o := append([]grpc.DialOption{}, opts...)
	o = append(o, []grpc.DialOption{
		grpc.WithDisableServiceConfig(),
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"loadBalancingConfig": [{"%s":%s}]}`, Name, string(grpcGCPjsonConfig))),
		grpc.WithChainUnaryInterceptor(GCPUnaryClientInterceptor),
		grpc.WithChainStreamInterceptor(GCPStreamClientInterceptor),
	}...)

	return o, nil
}

type monitoredConn struct {
	endpoint string
	conn     *grpc.ClientConn
	gme      *GCPMultiEndpoint
	cancel   context.CancelFunc
}

func (sm *monitoredConn) monitor() {
	var ctx context.Context
	ctx, sm.cancel = context.WithCancel(context.Background())
	currentState := sm.conn.GetState()
	for sm.conn.WaitForStateChange(ctx, currentState) {
		currentState = sm.conn.GetState()
		// Inform all multiendpoints.
		for _, me := range sm.gme.mes {
			me.SetEndpointAvailable(sm.endpoint, currentState == connectivity.Ready)
		}
	}
}

func (sm *monitoredConn) stopMonitoring() {
	sm.cancel()
}

// UpdateMultiEndpoints reconfigures MultiEndpoints.
func (gme *GCPMultiEndpoint) UpdateMultiEndpoints(meOpts *GCPMultiEndpointOptions) error {
	gme.Lock()
	defer gme.Unlock()
	if _, ok := meOpts.MultiEndpoints[meOpts.Default]; !ok {
		return fmt.Errorf("default MultiEndpoint %q missing options", meOpts.Default)
	}

	validPools := make(map[string]bool)
	for _, meo := range meOpts.MultiEndpoints {
		for _, e := range meo.Endpoints {
			validPools[e] = true
		}
	}

	// Add missing pools.
	for e := range validPools {
		if _, ok := gme.pools[e]; !ok {
			// This creates a ClientConn with the gRPC-GCP balancer managing connection pool.
			conn, err := grpcDial(e, gme.opts...)
			if err != nil {
				return err
			}
			gme.pools[e] = &monitoredConn{
				endpoint: e,
				conn:     conn,
				gme:      gme,
			}
			go gme.pools[e].monitor()
		}
	}

	// Add new multi-endpoints and update existing.
	for name, meo := range meOpts.MultiEndpoints {
		if me, ok := gme.mes[name]; ok {
			// Updating existing MultiEndpoint.
			me.SetEndpoints(meo.Endpoints)
			continue
		}

		// Add new MultiEndpoint.
		me, err := multiendpoint.NewMultiEndpoint(meo)
		if err != nil {
			return err
		}
		gme.mes[name] = me
	}
	gme.defaultName = meOpts.Default

	// Remove obsolete MultiEndpoints.
	for name := range gme.mes {
		if _, ok := meOpts.MultiEndpoints[name]; !ok {
			delete(gme.mes, name)
		}
	}

	// Remove obsolete pools.
	for e, mc := range gme.pools {
		if _, ok := validPools[e]; !ok {
			if err := mc.conn.Close(); err != nil {
				// TODO: log error.
			}
			mc.stopMonitoring()
			delete(gme.pools, e)
		}
	}

	// Trigger status update.
	for e, mc := range gme.pools {
		s := mc.conn.GetState()
		for _, me := range gme.mes {
			me.SetEndpointAvailable(e, s == connectivity.Ready)
		}
	}
	return nil
}

type multiError []error

func (m multiError) Error() string {
	s, n := "", 0
	for _, e := range m {
		if e != nil {
			if n == 0 {
				s = e.Error()
			}
			n++
		}
	}
	switch n {
	case 0:
		return "(0 errors)"
	case 1:
		return s
	case 2:
		return s + " (and 1 other error)"
	}
	return fmt.Sprintf("%s (and %d other errors)", s, n-1)
}

func (m multiError) Combine() error {
	if len(m) == 0 {
		return nil
	}

	return m
}
