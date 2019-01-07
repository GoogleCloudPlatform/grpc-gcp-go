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
	"os"
	"sync"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	"github.com/golang/protobuf/jsonpb"
	"google.golang.org/grpc"
)

type key int

var gcpKey key

type gcpContext struct {
	affinityCfg    grpc_gcp.AffinityConfig
	channelPoolCfg *grpc_gcp.ChannelPoolConfig
	reqMsg         interface{} // request message used for pre-process of an affinity call
	replyMsg       interface{} // response message used for post-process of an affinity call
}

// GCPInterceptor represents the interceptor for GCP specific features
type GCPInterceptor struct {
	channelPoolCfg   *grpc_gcp.ChannelPoolConfig
	methodToAffinity map[string]grpc_gcp.AffinityConfig // Maps method path to AffinityConfig
}

// NewGCPInterceptor creates a new GCPInterceptor with a given ApiConfig
func NewGCPInterceptor(config *grpc_gcp.ApiConfig) *GCPInterceptor {
	mp := make(map[string]grpc_gcp.AffinityConfig)
	methodCfgs := config.GetMethod()
	for _, methodCfg := range methodCfgs {
		methodNames := methodCfg.GetName()
		affinityCfg := methodCfg.GetAffinity()
		if methodNames != nil && affinityCfg != nil {
			for _, method := range methodNames {
				mp[method] = *affinityCfg
			}
		}
	}
	return &GCPInterceptor{
		channelPoolCfg:   config.GetChannelPool(),
		methodToAffinity: mp,
	}
}

// GCPUnaryClientInterceptor intercepts the execution of a unary RPC on the client using grpcgcp extension.
func (gcpInt *GCPInterceptor) GCPUnaryClientInterceptor(
	ctx context.Context,
	method string,
	req interface{},
	reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	affinityCfg, ok := gcpInt.methodToAffinity[method]
	if ok {
		gcpCtx := &gcpContext{
			affinityCfg:    affinityCfg,
			reqMsg:         req,
			replyMsg:       reply,
			channelPoolCfg: gcpInt.channelPoolCfg,
		}
		ctx = context.WithValue(ctx, gcpKey, gcpCtx)
	}

	return invoker(ctx, method, req, reply, cc, opts...)
}

// GCPStreamClientInterceptor intercepts the execution of a client streaming RPC using grpcgcp extension.
func (gcpInt *GCPInterceptor) GCPStreamClientInterceptor(
	ctx context.Context,
	desc *grpc.StreamDesc,
	cc *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	// This constructor does not create a real ClientStream,
	// it only stores all parameters and let SendMsg() to create ClientStream.
	cs := &gcpClientStream{
		gcpInt:   gcpInt,
		ctx:      ctx,
		desc:     desc,
		cc:       cc,
		method:   method,
		streamer: streamer,
		opts:     opts,
	}
	cs.cond = sync.NewCond(cs)
	return cs, nil
}

type gcpClientStream struct {
	sync.Mutex
	grpc.ClientStream

	cond     *sync.Cond
	gcpInt   *GCPInterceptor
	ctx      context.Context
	desc     *grpc.StreamDesc
	cc       *grpc.ClientConn
	method   string
	streamer grpc.Streamer
	opts     []grpc.CallOption
}

func (cs *gcpClientStream) SendMsg(m interface{}) error {
	// Initialize the underlying client stream when getting the first request message.
	if cs.ClientStream == nil {
		affinityCfg, ok := cs.gcpInt.methodToAffinity[cs.method]
		ctx := cs.ctx
		if ok {
			gcpCtx := &gcpContext{
				affinityCfg:    affinityCfg,
				reqMsg:         m,
				channelPoolCfg: cs.gcpInt.channelPoolCfg,
			}
			ctx = context.WithValue(cs.ctx, gcpKey, gcpCtx)
		}
		realCS, err := cs.streamer(ctx, cs.desc, cs.cc, cs.method, cs.opts...)
		if err != nil {
			return err
		}
		cs.Lock()
		cs.ClientStream = realCS
		cs.Unlock()
		cs.cond.Broadcast()
	}
	return cs.ClientStream.SendMsg(m)
}

func (cs *gcpClientStream) RecvMsg(m interface{}) error {
	// If RecvMsg is called before SendMsg, it should wait until cs.ClientStream
	// is initialized, to avoid nil pointer error.
	cs.Lock()
	for cs.ClientStream == nil {
		cs.cond.Wait()
	}
	cs.Unlock()
	return cs.ClientStream.RecvMsg(m)
}

// ParseAPIConfig parses a json config file into ApiConfig proto message.
func ParseAPIConfig(path string) (*grpc_gcp.ApiConfig, error) {
	jsonFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	pb := &grpc_gcp.ApiConfig{}
	jsonpb.Unmarshal(jsonFile, pb)
	return pb, nil
}
