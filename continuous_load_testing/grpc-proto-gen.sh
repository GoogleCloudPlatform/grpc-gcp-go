go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
export PATH="$PATH:$(go env GOPATH)/bin"
mkdir -p proto
protoc --proto_path=../third_party/grpc-proto \
--proto_path=proto \
--go_out=proto \
--go_opt=module=continuous_load_testing/proto \
--go_opt=Mgrpc/testing/empty.proto=continuous_load_testing/proto/grpc/testing/empty \
--go_opt=Mgrpc/testing/messages.proto=continuous_load_testing/proto/grpc/testing/messages \
--go_opt=Mgrpc/testing/test.proto=continuous_load_testing/proto/grpc/testing/test \
--go_opt=Mgrpc_gcp/testing/test.proto=continuous_load_testing/proto/grpc_gcp/testing/test \
--go-grpc_out=proto \
--go-grpc_opt=module=continuous_load_testing/proto \
--go-grpc_opt=Mgrpc/testing/empty.proto=continuous_load_testing/proto/grpc/testing/empty \
--go-grpc_opt=Mgrpc/testing/messages.proto=continuous_load_testing/proto/grpc/testing/messages \
--go-grpc_opt=Mgrpc/testing/test.proto=continuous_load_testing/proto/grpc/testing/test \
--go-grpc_opt=Mgrpc_gcp/testing/test.proto=continuous_load_testing/proto/grpc_gcp/testing/test \
grpc/testing/empty.proto \
grpc/testing/messages.proto \
grpc/testing/test.proto \
grpc_gcp/testing/test.proto