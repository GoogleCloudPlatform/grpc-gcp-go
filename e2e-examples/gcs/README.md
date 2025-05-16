## Regenerating protos

```sh
git clone git@github.com:googleapis/googleapis.git
cd googleapis
protoc --go_out=.. --go-grpc_out=.. google/storage/v2/*.proto
cd ..
```

## Example commands

Use grpc client to write a 128KiB file to a GCS bucket

```sh
go run main.go --method=write --size=128
```

Use grpc client to read a 128KiB file from a GCS bucket

```sh
go run main.go --method=read --size=128
```
