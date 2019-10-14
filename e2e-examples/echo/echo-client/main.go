package main

import (
	"context"
	"flag"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/GoogleCloudPlatform/grpc-gcp-go/e2e-examples/echo/echo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	addr    = flag.String("addr", "staging-grpc-cfe-benchmarks.googleapis.com:443", "server address")
	numRpcs = flag.Int("numRpcs", 1, "number of blocking unary calls")
	warmup  = flag.Int("warmup", 5, "number of warmup calls before test")
	rspSize = flag.Int("rspSize", 1, "response size in KB")
	reqSize = flag.Int("reqSize", 1, "request size in KB")
	async   = flag.Bool("async", false, "use async echo calls")
)

func printRsts(numRpcs int, rspSize int, reqSize int, rsts []int) {
	sort.Ints(rsts)
	n := len(rsts)
	sum := 0
	for _, r := range rsts {
		sum += r
	}
	log.Printf(
		"\n[Number of RPCs: %v, Request size: %vKB, Response size: %vKB]\n"+
			"\t\tAvg\tMin\tp50\tp90\tp99\tMax\n"+
			"Time(ms)\t%v\t%v\t%v\t%v\t%v\t%v\n",
		numRpcs, reqSize, rspSize,
		sum/n,
		rsts[0],
		rsts[int(float64(n)*0.5)],
		rsts[int(float64(n)*0.9)],
		rsts[int(float64(n)*0.99)],
		rsts[n-1],
	)
}

func main() {
	flag.Parse()
	conn, err := grpc.Dial(
		*addr,
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
		grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewGrpcCloudapiClient(conn)
	msg := strings.Repeat("x", *reqSize*1024)
	req := &pb.EchoWithResponseSizeRequest{EchoMsg: msg, ResponseSize: int32(*rspSize * 1024)}

	// begin warmup calls
	for i := 0; i < *warmup; i++ {
		_, err := client.EchoWithResponseSize(context.Background(), req)
		if err != nil {
			log.Fatalf("EchoWithResponseSize failed with error during warmup: %v", err)
		}
	}

	rsts := []int{}

	if !*async {
		// begin tests
		for i := 0; i < *numRpcs; i++ {
			start := time.Now()
			_, err := client.EchoWithResponseSize(context.Background(), req)
			if err != nil {
				log.Fatalf("EchoWithResponseSize failed with error: %v", err)
			}
			rsts = append(rsts, int(time.Since(start).Milliseconds()))
		}
		printRsts(*numRpcs, *rspSize, *reqSize, rsts)
	} else {
		var wg sync.WaitGroup
		wg.Add(*numRpcs)

		for i := 0; i < *numRpcs; i++ {
			go func(r *pb.EchoWithResponseSizeRequest, n int) {
				defer wg.Done()
				_, err := client.EchoWithResponseSize(context.Background(), r)
				if err != nil {
					log.Fatalf("EchoWithResponseSize failed with error: %v", err)
				}
				log.Printf("Done %vth request", n)
			}(req, i)
		}
		wg.Wait()
	}
}
