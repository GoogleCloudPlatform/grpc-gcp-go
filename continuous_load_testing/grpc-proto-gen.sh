go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
export PATH="$PATH:$(go env GOPATH)/bin"
mkdir -p proto
protoc --proto_path=proto \
--go_out=proto \
--go_opt=module=continuous_load_testing/proto \
--go_opt=Mgrpc_gcp/testing/empty.proto=continuous_load_testing/proto/grpc_gcp/testing/empty \
--go_opt=Mgrpc_gcp/testing/messages.proto=continuous_load_testing/proto/grpc_gcp/testing/messages \
--go_opt=Mgrpc_gcp/testing/test.proto=continuous_load_testing/proto/grpc_gcp/testing/test \
--go-grpc_out=proto \
--go-grpc_opt=module=continuous_load_testing/proto \
--go-grpc_opt=Mgrpc_gcp/testing/empty.proto=continuous_load_testing/proto/grpc_gcp/testing/empty \
--go-grpc_opt=Mgrpc_gcp/testing/messages.proto=continuous_load_testing/proto/grpc_gcp/testing/messages \
--go-grpc_opt=Mgrpc_gcp/testing/test.proto=continuous_load_testing/proto/grpc_gcp/testing/test \
grpc_gcp/testing/empty.proto \
grpc_gcp/testing/messages.proto \
grpc_gcp/testing/test.proto
