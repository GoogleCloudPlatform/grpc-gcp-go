package main

import (
	"context"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"time"

	gcspb "github.com/GoogleCloudPlatform/grpc-gcp-go/e2e-examples/gcs/cloud.google.com/go/storage/genproto/apiv2/storagepb"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/balancer/rls"
	"google.golang.org/grpc/credentials/google"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"
	_ "google.golang.org/grpc/xds/googledirectpath"
)

var (
	dp           = flag.Bool("dp", true, "whether use directpath")
	td           = flag.Bool("td", true, "whether use traffic director")
	host         = flag.String("host", "storage.googleapis.com", "gcs backend hostname")
	clientType   = flag.String("client", "go-grpc", "type of client")
	objectFormat = flag.String("obj_format", "write/128KiB/128KiB", "gcs object name")
	bucketName   = flag.String("bkt", "gcs-grpc-team-dp-test-us-central1", "gcs bucket name")
	numCalls     = flag.Int("calls", 1, "num of calls")
	numWarmups   = flag.Int("warmups", 1, "num of warmup calls")
	numThreads   = flag.Int("threads", 1, "num of threads")
	method       = flag.String("method", "write", "method names")
	size         = flag.Int("size", 128, "write size in kb")
)

func getBucketName() string {
	return fmt.Sprintf("projects/_/buckets/%s", *bucketName)
}

func addBucketMetadataToCtx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		"x-goog-request-params",
		fmt.Sprintf("bucket=%s", getBucketName()))
}

func getGrpcClient() gcspb.StorageClient {
	resolver.SetDefaultScheme("dns")
	endpoint := fmt.Sprintf("google-c2p:///%s", *host)

	var grpcOpts []grpc.DialOption
	grpcOpts = []grpc.DialOption{
		grpc.WithCredentialsBundle(
			google.NewComputeEngineCredentials(),
		),
	}
	conn, err := grpc.Dial(endpoint, grpcOpts...)
	if err != nil {
		fmt.Println("Failed to create clientconn: %v", err)
		os.Exit(1)
	}
	return gcspb.NewStorageClient(conn)
}

func readRequest(client gcspb.StorageClient) {
	ctx := context.Background()
	req := gcspb.ReadObjectRequest{
		Bucket: getBucketName(),
		Object: *objectFormat,
	}
	ctx = addBucketMetadataToCtx(ctx)
	start := time.Now()
	stream, err := client.ReadObject(ctx, &req)
	if err != nil {
		fmt.Println("ReadObject got error: ", err)
		os.Exit(1)
	}
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			fmt.Println("Done reading object.")
			break
		}
		if err != nil {
			fmt.Println("ReadObject Recv error: ", err)
			os.Exit(1)
		}
		len := len(resp.GetChecksummedData().GetContent())
		fmt.Printf("bytes read: %d\n", len)
	}
	total := time.Since(start).Milliseconds()
	fmt.Println("total time in ms for read: ", total)
}

func getWriteRequest(isFirst bool, isLast bool, offset int64, data []byte) *gcspb.WriteObjectRequest {
	crc32c := crc32.MakeTable(crc32.Castagnoli)
	checksum := crc32.Checksum(data, crc32c)

	req := &gcspb.WriteObjectRequest{}
	if isFirst {
		req.FirstMessage = &gcspb.WriteObjectRequest_WriteObjectSpec{
			WriteObjectSpec: &gcspb.WriteObjectSpec{
				Resource: &gcspb.Object{
					Bucket: getBucketName(),
					Name:   *objectFormat,
				},
			},
		}
	}
	req.WriteOffset = offset
	req.Data = &gcspb.WriteObjectRequest_ChecksummedData{
		ChecksummedData: &gcspb.ChecksummedData{
			Content: data,
			Crc32C:  &checksum,
		},
	}
	if isLast {
		req.FinishWrite = true
	}
	return req
}

func writeRequest(client gcspb.StorageClient) {
	ctx := context.Background()
	ctx = addBucketMetadataToCtx(ctx)

	totalBytes := *size * 1024
	offset := 0
	isFirst := true
	isLast := false

	start := time.Now()
	stream, err := client.WriteObject(ctx)
	if err != nil {
		fmt.Println("WriteObject got error: ", err)
		os.Exit(1)
	}

	for offset < totalBytes {
		var add int
		if offset+int(gcspb.ServiceConstants_MAX_WRITE_CHUNK_BYTES) <= totalBytes {
			add = int(gcspb.ServiceConstants_MAX_WRITE_CHUNK_BYTES)
		} else {
			add = totalBytes - offset
		}
		if offset+add == totalBytes {
			isLast = true
		}
		data := make([]byte, add, add)
		req := getWriteRequest(isFirst, isLast, int64(offset), data)
		fmt.Printf("writing %d bytes\n", add)
		if err := stream.Send(req); err != nil {
			fmt.Println("stream.Send got error: ", err)
		}
		isFirst = false
		offset += add
	}
	stream.CloseAndRecv()
	total := time.Since(start).Milliseconds()
	fmt.Println("total time in ms for write: ", total)
}

func main() {
	flag.Parse()
	if !*dp {
		fmt.Println("only directpath is supported for now")
		os.Exit(1)
	}
	if !*td {
		fmt.Println("only traffic director is supported for now")
		os.Exit(1)
	}

	var client gcspb.StorageClient
	switch *clientType {
	case "go-grpc":
		client = getGrpcClient()
	default:
		fmt.Printf("Unsupported --client=%s\n", *clientType)
		os.Exit(1)
	}

	switch *method {
	case "write":
		writeRequest(client)
	case "read":
		readRequest(client)
	default:
		fmt.Printf("Unsupported --method=%s\n", *method)
		os.Exit(1)
	}
}
