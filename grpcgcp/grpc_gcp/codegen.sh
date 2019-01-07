#!/usr/bin/env bash
cd "$(dirname "$0")"

rm grpc_gcp.pb.go
protoc --plugin=$GOPATH/bin/protoc-gen-go --proto_path=./ --go_out=./ ./grpc_gcp.proto

