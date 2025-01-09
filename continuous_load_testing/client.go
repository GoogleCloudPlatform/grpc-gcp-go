//go:generate mkdir -p proto
//go:generate protoc --proto_path=../third_party/grpc-proto --go_out=proto --go_opt=Mgrpc/testing/empty.proto=grpc.io/grpc-proto/grpc/testing/empty --go_opt=Mgrpc/testing/messages.proto=grpc.io/grpc-proto/grpc/testing/messages --go_opt=Mgrpc/testing/test.proto=grpc.io/grpc-proto/grpc/testing/test grpc/testing/empty.proto grpc/testing/messages.proto grpc/testing/test.proto

package main

func main() {

}
