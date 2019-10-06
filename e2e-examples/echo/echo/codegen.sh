#!/usr/bin/env bash
cd "$(dirname "$0")"

#protoc --plugin=$GOPATH/bin/protoc-gen-go --proto_path=./ --go_out=./ ./echo.proto

protoc --go_out=plugins=grpc:. *.proto

