# Echo Client for e2e test

Simple echo client for e2e testing against the echo service defined in
[echo.proto](echo/echo.proto).

## Usage

The client sends out `numRpcs` number of Ping-Pong unary requests sequentially
with request size specified by `reqSize` in KB, and response size specified by
`rspSize` in KB, test result will be printed in console.

## Generate protobuf code

If we need to regenerate pb code for echo grpc service, run:

```sh
./echo/codegen.sh
```

## Run client

Example command for endpoint `some.test.service` with 100 RPCs and 100KB
response size:

```sh
go run echo-client/main.go -numRpcs=100 -rspSize=100
```

Example test result

```sh
[Number of RPCs: 100, Request size: 1KB, Response size: 100KB]
                Avg     Min     p50     p90     p99     Max
Time(ms)        76      74      76      78      109     109
```
