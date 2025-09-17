go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

export PATH="$PATH:$(go env GOPATH)/bin"
mkdir -p proto
## The module path from your go.mod file, with the /proto suffix
#MODULE_PATH="github.com/GoogleCloudPlatform/grpc-gcp-go/continuous_load_testing/proto"

#protoc --proto_path=proto \
#--go_out=proto \
#--go_opt=module=continuous_load_testing/proto \
#--go_opt=Mgrpc_gcp/testing/empty.proto=continuous_load_testing/proto/grpc_gcp/testing/empty \
#--go_opt=Mgrpc_gcp/testing/messages.proto=continuous_load_testing/proto/grpc_gcp/testing/messages \
#--go_opt=Mgrpc_gcp/testing/test.proto=continuous_load_testing/proto/grpc_gcp/testing/test \
#--go-grpc_out=proto \
#--go-grpc_opt=module=continuous_load_testing/proto \
#--go-grpc_opt=Mgrpc_gcp/testing/empty.proto=continuous_load_testing/proto/grpc_gcp/testing/empty \
#--go-grpc_opt=Mgrpc_gcp/testing/messages.proto=continuous_load_testing/proto/grpc_gcp/testing/messages \
#--go-grpc_opt=Mgrpc_gcp/testing/test.proto=continuous_load_testing/proto/grpc_gcp/testing/test \

#proto/grpc_gcp/testing/empty.proto \
#proto/grpc_gcp/testing/messages.proto \
#proto/grpc_gcp/testing/test.proto


MODULE_PATH="github.com/GoogleCloudPlatform/grpc-gcp-go/continuous_load_testing/proto"

protoc --proto_path=proto \
    --go_out=proto \
    --go_opt=module=${MODULE_PATH} \
    --go-grpc_out=proto \
    --go-grpc_opt=module=${MODULE_PATH} \
    proto/grpc_gcp/testing/empty.proto \
    proto/grpc_gcp/testing/messages.proto \
    proto/grpc_gcp/testing/test.proto

echo "Successfully generated Go protobuf files."
