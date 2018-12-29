package grpc_gcp

import (
	"context"
	"sync"

	"google.golang.org/grpc"
)

type key int

var gcpKey key

type gcpContext struct {
	affinityCfg    AffinityConfig
	reqMsg         interface{}
	replyMsg       interface{}
	channelPoolCfg *ChannelPoolConfig
}

// GCPInterceptor represents the interceptor for GCP specific features
type GCPInterceptor struct {
	channelPoolCfg   *ChannelPoolConfig
	methodToAffinity map[string]AffinityConfig
}

// NewGCPInterceptor creates a new GCPInterceptor with a given ApiConfig
func NewGCPInterceptor(config ApiConfig) *GCPInterceptor {
	mp := make(map[string]AffinityConfig)
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
