package grpc_gcp

import (
	"google.golang.org/grpc"
	"context"
)

type key int

var gcpKey key

type gcpContext struct {
	affinityCfg AffinityConfig
	reqMsg interface{}
	replyMsg interface{}
}

// GCPInterceptor represents the interceptor for GCP specific features
type GCPInterceptor struct {
	apiConfig ApiConfig
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
		apiConfig: config,
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
		gcpCxt := & gcpContext{
			affinityCfg: affinityCfg,
			reqMsg: req,
			replyMsg: reply,
		}
		ctx = context.WithValue(ctx, gcpKey, gcpCxt)
	}

	return invoker(ctx, method, req, reply, cc, opts...)
}
