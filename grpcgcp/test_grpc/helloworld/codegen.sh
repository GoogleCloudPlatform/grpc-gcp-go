#!/usr/bin/env bash
cd "$(dirname "$0")"

rm -r helloworld
protoc --plugin=$(go env GOPATH)/bin/protoc-gen-go --proto_path=./ --go_out=plugins=grpc:. ./helloworld.proto
