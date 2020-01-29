package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"cloud.google.com/go/storage"

	gcspb "github.com/GoogleCloudPlatform/grpc-gcp-go/e2e-examples/gcs/google.golang.org/genproto/googleapis/storage/v1"
	_ "google.golang.org/grpc/balancer/grpclb"
	grpcgoogle "google.golang.org/grpc/credentials/google"
	"google.golang.org/grpc/credentials/oauth"
)

const (
	target = "storage.googleapis.com:443"
	scope  = "https://www.googleapis.com/auth/cloud-platform"
)

var (
	dp         = flag.Bool("dp", false, "whether use directpath")
	corp       = flag.Bool("corp", false, "whether calling from corp machine")
	useHttp    = flag.Bool("http", false, "whether to use http client")
	objectName = flag.String("obj", "a", "gcs object name")
	bucketName = flag.String("bkt", "gcs-grpc-team-weiranf", "gcs bucket name")
	numCalls   = flag.Int("calls", 1, "num of calls")
	uploadSize = flag.Int("upload", 0, "upload size in kb")
	cookie     = flag.String("cookie", "", "cookie header")
)

func upload(client *storage.Client, kb int) {
	ctx := context.Background()
	obj := client.Bucket(*bucketName).Object(*objectName)
	w := obj.NewWriter(ctx)
	msg := strings.Repeat("x", kb*1024)
	if _, err := fmt.Fprint(w, msg); err != nil {
		fmt.Println("Failed to write message to object: %v", err)
	}
	if err := w.Close(); err != nil {
		fmt.Println("object writer failed closing: %v", err)
		os.Exit(1)
	}
}

func getGrpcClient() gcspb.StorageClient {

	var grpcOpts []grpc.DialOption
	endpoint := target

	if *dp {
		endpoint = "dns:///" + target
		grpcOpts = []grpc.DialOption{
			grpc.WithCredentialsBundle(
				grpcgoogle.NewComputeEngineCredentials(),
			),
			grpc.WithDisableServiceConfig(),
			grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"grpclb":{"childPolicy":[{"pick_first":{}}]}}]}`),
		}
	} else if *corp {
		// client is calling from corp machine
		keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		perRPC, err := oauth.NewServiceAccountFromFile(keyFile, scope)
		if err != nil {
			fmt.Println("Failed to create credentials: %v", err)
			os.Exit(1)
		}
		grpcOpts = []grpc.DialOption{
			grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
			grpc.WithPerRPCCredentials(perRPC),
		}
	} else {
		// client is calling from GCE
		grpcOpts = []grpc.DialOption{
			grpc.WithCredentialsBundle(
				grpcgoogle.NewComputeEngineCredentials(),
			),
		}
	}

	cc, err := grpc.Dial(endpoint, grpcOpts...)

	if err != nil {
		fmt.Println("Failed to create clientconn: %v", err)
		os.Exit(1)
	}

	return gcspb.NewStorageClient(cc)
}

func getHttpClient() *storage.Client {
	ctx := context.Background()
	httpClient, err := storage.NewClient(ctx)
	if err != nil {
		fmt.Println("Failed to create http client: %v", err)
		os.Exit(1)
	}
	return httpClient
}

func makeGrpcRequest(client gcspb.StorageClient) []int {
	fmt.Println("========================== start grpc calls ===============================")
	res := []int{}

	req := gcspb.GetObjectMediaRequest{
		Bucket: *bucketName,
		Object: *objectName,
	}

	for i := 0; i < *numCalls; i++ {
		ctx := context.Background()
		if i == *numCalls-1 {
			md := metadata.Pairs("cookie", *cookie)
			ctx = metadata.NewOutgoingContext(ctx, md)
		}

		stream, err := client.GetObjectMedia(ctx, &req)
		if err != nil {
			fmt.Println("GetObjectMedia got error: ", err)
			os.Exit(1)
		}

		start := time.Now()
		for {
			_, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Println("stream.Recv() got error: ", err)
				os.Exit(1)
			}
			//fmt.Printf("rsp: %+v\n", rsp)
		}
		total := time.Since(start).Milliseconds()
		res = append(res, int(total))
		fmt.Println("total time in ms for GetObjectMedia: ", total)
	}
	return res
}

func makeJsonRequest(client *storage.Client) []int {
	fmt.Println("========================== start http calls ===============================")
	res := []int{}

	for i := 0; i < *numCalls; i++ {
		start := time.Now()
		obj := client.Bucket(*bucketName).Object(*objectName)
		rc, err := obj.NewReader(context.Background())
		if err != nil {
			fmt.Println("Failed to create object reader: %v", err)
			os.Exit(1)
		}
		defer rc.Close()

		_, err = ioutil.ReadAll(rc)
		if err != nil {
			fmt.Println("Failed to read data from object: %v", err)
			os.Exit(1)
		}
		total := time.Since(start).Milliseconds()
		res = append(res, int(total))
		//fmt.Printf("http object data: %s\n", data)
		fmt.Println("total time in ms for http call: ", total)
	}
	return res
}

func printResult(res []int) {
	sort.Ints(res)
	n := len(res)
	sum := 0
	for _, r := range res {
		sum += r
	}
	fmt.Printf(
		"\n\t\tAvg\tMin\tp50\tp90\tp99\tMax\n"+
			"Time(ms)\t%v\t%v\t%v\t%v\t%v\t%v\n",
		sum/n,
		res[0],
		res[int(float64(n)*0.5)],
		res[int(float64(n)*0.9)],
		res[int(float64(n)*0.99)],
		res[n-1],
	)
}

func main() {
	flag.Parse()
	if *uploadSize > 0 {
		httpClient := getHttpClient()
		upload(httpClient, *uploadSize)
		return
	}
	var res []int
	if *useHttp {
		httpClient := getHttpClient()
		res = makeJsonRequest(httpClient)
	} else {
		grpcClient := getGrpcClient()
		res = makeGrpcRequest(grpcClient)
	}
	printResult(res)
}
