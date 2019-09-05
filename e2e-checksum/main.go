package main

import (
	"context"
	"hash/crc32"
	"log"

	"cloud.google.com/go/datastore"
	"github.com/golang/protobuf/proto"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	protoCodec "google.golang.org/grpc/encoding/proto"
)

const (
	checksumField    = 2047
	checksumWireType = 5 // wire type is a 32-bit
)

type myCodec struct {
	protoCodec encoding.Codec
}

func (c *myCodec) Marshal(v interface{}) ([]byte, error) {
	bytes, err := c.protoCodec.Marshal(v)
	if err != nil {
		return bytes, err
	}
	crc32c := crc32.MakeTable(crc32.Castagnoli)
	checksum := crc32.Checksum(bytes, crc32c)

	buffer := proto.NewBuffer([]byte{})

	// calculate checksum tag (field & wire type)
	tag := (checksumField << 3) | checksumWireType

	if err = buffer.EncodeVarint(uint64(tag)); err != nil {
		return bytes, err
	}

	if err = buffer.EncodeFixed32(uint64(checksum)); err != nil {
		return bytes, err
	}

	log.Printf("encoded checksum field and value: %+v\n", buffer.Bytes())
	newBytes := append(buffer.Bytes(), bytes...) // prepend
	//newBytes := append(bytes, buffer.Bytes()...) // append
	log.Printf("Marshalled bytes: %+v\n", bytes)
	return newBytes, err
}

func (c *myCodec) Unmarshal(data []byte, v interface{}) error {
	return c.protoCodec.Unmarshal(data, v)
}

func (c *myCodec) String() string {
	return "MyCodec"
}

func main() {
	type Entity struct {
		Firstname string
		Lastname  string
	}

	ctx := context.Background()
	projectID := "grpc-gcp"
	opts := []option.ClientOption{
		option.WithGRPCDialOption(grpc.WithCodec(&myCodec{protoCodec: encoding.GetCodec(protoCodec.Name)})),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, ""))),
		//option.WithEndpoint("stubby-e2e-generic-service-test.sandbox.googleapis.com:443"),
	}
	client, err := datastore.NewClient(ctx, projectID, opts...)
	if err != nil {
		log.Fatalf("Failed to create firestore client: %v", err)
	}
	kind := "Person"
	name := "weiranf"
	key := datastore.NameKey(kind, name, nil)

	e := Entity{
		Firstname: "Weiran",
		Lastname:  "Fang",
	}

	if _, err := client.Put(ctx, key, &e); err != nil {
		log.Fatalf("client.Put failed: %v", err)
	}
}
