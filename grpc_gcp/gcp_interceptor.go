package grpc_gcp

import (
	"fmt"
	"google.golang.org/grpc"
	// "google.golang.org/grpc/metadata"
	"context"
	// "github.com/golang/protobuf/proto"
	"reflect"
	"strings"
)

type key int

var gcpKey key

type gcpContext struct {
	affinityKey string
	affinityCmd AffinityConfig_Command
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
		locator := affinityCfg.GetAffinityKey()
		ak := ""
		cmd := affinityCfg.GetCommand()
		if cmd == AffinityConfig_BOUND || cmd == AffinityConfig_UNBIND {
			a, err := getAffinityKeyFromMessage(locator, req)
			if err != nil {
				return fmt.Errorf("failed to retrieve affinity key from message: %v", err)
			}
			ak = a
		}
		gcpCxt := & gcpContext{
			affinityKey: ak,
			affinityCmd: cmd,
		}
		ctx = context.WithValue(ctx, gcpKey, gcpCxt)
	}

	return invoker(ctx, method, req, reply, cc, opts...)
}

func getAffinityKeyFromMessage(locator string, msg interface{}) (affinityKey string, err error) {
	if locator == "" {
		return "", fmt.Errorf("affinityKey locator is not valid")
	}

	names := strings.Split(locator, ".")
	val := reflect.ValueOf(msg).Elem()

	var ak string
	i := 0
	for ; i < len(names); i++ {
		name := names[i]
		titledName := strings.Title(name)
		valField := val.FieldByName(titledName)
		if valField.IsValid() {
			switch valField.Kind() {
			case reflect.String:
				ak = valField.String()
				break
			case reflect.Interface:
				val = reflect.ValueOf(valField.Interface())
			default:
				return "", fmt.Errorf("field %s in message is neither a string nor another message", titledName)
			}
		}
	}
	if i == len(names) {
		return ak, nil
	}
	
	return "", fmt.Errorf("cannot get valid affinity key")
}