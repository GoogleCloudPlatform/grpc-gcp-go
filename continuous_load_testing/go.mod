module continuous_load_testing

go 1.23

require google.golang.org/grpc v1.69.2

require (
	golang.org/x/net v0.30.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/text v0.19.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241015192408-796eee8c2d53 // indirect
	google.golang.org/protobuf v1.35.1 // indirect
)
replace (
	"grpc.io/grpc/testing/empty" => "./proto/grpc.io/grpc/testing/empty"
	"grpc.io/grpc/testing/messages" => "./proto/grpc.io/grpc/testing/messages"
	"grpc.io/grpc/testing/test" => "./proto/grpc.io/grpc/testing/test"
)
