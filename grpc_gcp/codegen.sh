#!/usr/bin/env bash
cd "$(dirname "$0")"

rm grpc_gcp.pb.go
protoc --proto_path=./protos --go_out=./ ./protos/grpc_gcp.proto

