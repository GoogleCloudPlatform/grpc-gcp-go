# Instructions for create a gRPC client for google cloud services

## Overview

This instruction includes a step by step guide for creating a gRPC 
client to test the google cloud service from an empty linux 
VM, using GCE ubuntu 16.04 TLS instance.

The main steps are followed as steps below: 

- Environment prerequisite
- Install gRPC-go, plugin, protobuf and oauth2
- Generate client API from .proto files
- Create the client and send/receive RPC.

## Environment Prerequisite

**Golang**
```sh
$ wget https://dl.google.com/go/go1.9.3.linux-amd64.tar.gz
$ [sudo] tar -C /usr/local -xzf go1.9.3.linux-amd64.tar.gz
$ echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc
$ source ~/.bashrc
$ mkdir $HOME/go
```

## Install gRPC-go, plugin, protobuf and oauth2
- gRPC-go, plugin and pauth2
```sh
$ cd $HOME/go
$ go get -u google.golang.org/grpc
$ go get -u github.com/golang/protobuf/protoc-gen-go
$ go get golang.org/x/oauth2/google
```
- protobuf
```sh
$ cd $HOME
$ git clone https://github.com/google/protobuf.git
$ cd $HOME/protobuf
$ ./autogen.sh && ./configure && make -j8
$ [sudo] make install
$ [sudo] ldconfig
```

## Generate client API from .proto files 
Please check files under `$HOME/go/src/google.golang.org/genproto/googleapis` to see whether
you service client API **has already been generated**. 
If they are already there, you can skip this step. 
For most google cloud APIs, all client APIs are already generated in the
[go-genproto repo](https://github.com/google/go-genproto) under `googleapis/`.

**If they have not already been generated**, 
the common way to use the plugin looks like
```sh
$ mkdir $HOME/go/src/project-golang && cd $HOME/go/src/project-golang
$ protoc --proto_path=/path/to/proto_dir --go_out=./\  
path/to/your/proto_dependency_directory1/*.proto \
path/to/your/proto_dependency_directory2/*.proto \
path/to/your/proto_service_directory/*.proto
```

Assume that you don't need to generate pb files
because you find them generated under `$HOME/go/src/google.golang.org/genproto/googleapis`.
They are installing during installing the gRPC.
Take [`Firestore`](https://github.com/googleapis/googleapis/blob/master/google/firestore/v1beta1/firestore.proto)
as example, the Client API is under 
`$HOME/go/src/google.golang.org/genproto/googleapis/firestore/v1beta1/firestore.pb.go` depends on your 
package namespace inside .proto file. An easy way to find your client is 
```sh
$ cd $HOME/go
$ find ./ -name [service_name: eg, firestore, cluster_service]*
```
The one under `genproto` directory is what you need.

## Create the client and send/receive RPC.
Now it's time to use the client API to send and receive RPCs.

Here I assume that you don't need to generate pb files from the last step
and use files under `$HOME/go/src/google.golang.org/genproto/googleapis` directly.
If you generate them by your own, the difference is change the import path.

**Set credentials file**

This is important otherwise your RPC response will be a permission error.
``` sh
$ vim $HOME/key.json
## Paste you credential file downloaded from your cloud project
## which you can find in APIs&Services => credentials => create credentials
## => Service account key => your credentials
$ export GOOGLE_APPLICATION_CREDENTIALS=$HOME/key.json
```

**Implement Service Client**

Take a unary-unary RPC `listDocument` from `FirestoreClient` as example.
Create a file name `$HOME/src/main.go`.
- Import library
```
package main

import (
        "context"
        "fmt"
        "log"
        "os"

        "google.golang.org/grpc"
        "google.golang.org/grpc/credentials"
        "google.golang.org/grpc/credentials/oauth"

        firestore "google.golang.org/genproto/googleapis/firestore/v1beta1"
)
```
- Set Google Auth. Please see the referece for 
[authenticate with Google using an Oauth2 token](https://grpc.io/docs/guides/auth.html#authenticate-with-google) for the use of 'googleauth' library.
```
keyFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
perRPC, err := oauth.NewServiceAccountFromFile(keyFile, "https://www.googleapis.com/auth/datastore")
if err != nil {
        log.Fatalf("Failed to create credentials: %v", err)
}
address := "firestore.googleapis.com:443"
conn, err := grpc.Dial(address, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")), grpc.WithPerRPCCredentials(perRPC))
defer conn.Close()
```
- Create Client
```
client := firestore.NewFirestoreClient(conn)
```
- Make and receive RPC call
```
listDocsRequest := firestore.ListDocumentsRequest{
        Parent: "projects/ddyihai-firestore/databases/(default)",
}
resp, err := client.ListDocuments(context.Background(), &listDocsRequest)
if err != nil {
        fmt.Println(err)
}
```
- Print RPC response
```
for _, doc := range resp.Documents {
        fmt.Printf("%+v\n", doc)
}
```
- Run the script
```sh
$ cd $HOME/go
$ go run src/main.go
```

For different kinds of RPC(unary-unary, unary-stream, stream-unary, stream-stream),
please check [grpc.io Golang part](https://grpc.io/docs/tutorials/basic/go.html#simple-rpcc)
for reference.


