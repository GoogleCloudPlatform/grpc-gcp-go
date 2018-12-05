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
}

// GCPInterceptor represents the interceptor for GCP specific features
type GCPInterceptor struct {
	apiConfig ApiConfig
}

// GCPUnaryClientInterceptor intercepts the execution of a unary RPC on the client using grpcgcp extension.
func (*GCPInterceptor) GCPUnaryClientInterceptor(
	ctx context.Context,
	method string,
	req interface{},
	reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	// TODO: pass in affinity config
	affinityKey := "name"
	names := strings.Split(affinityKey, ".")
	val := reflect.ValueOf(req).Elem()

	var ak string
	
	for _, name := range names {
		titledName := strings.Title(name)
		valField := val.FieldByName(titledName)
		if valField.IsValid() {
			switch valField.Kind() {
			case reflect.String:
				ak = valField.String()
				fmt.Println("*** Found affinity key:", ak)
				break
			case reflect.Interface:
				val = reflect.ValueOf(valField.Interface())
			default:
				return fmt.Errorf("Field %s in message is neither a string nor another message", titledName)
			}
			fmt.Println(valField.Kind())
		}
	}
	
	// TODO: throw error for invalid affinity path
	// if ak == "" {
	// 	return fmt.Errorf("Affinity key %s for method %s is a valid path", affinityKey, method)
	// }
	ctx = context.WithValue(ctx, gcpKey, &gcpContext{affinityKey: ak})

	err := invoker(ctx, method, req, reply, cc, opts...)
	return err
}