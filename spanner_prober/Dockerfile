FROM golang:1.19
WORKDIR /go/src/github.com/GoogleCloudPlatform/grpc-gcp-go/spanner_prober/
COPY . .
RUN CGO_ENABLED=0 go build -a -installsuffix cgo -v -o /go/src/github.com/GoogleCloudPlatform/grpc-gcp-go/spanner_prober ./...

FROM alpine:latest  
RUN apk --no-cache add ca-certificates
WORKDIR /spanner_prober/
COPY --from=0 /go/src/github.com/GoogleCloudPlatform/grpc-gcp-go/spanner_prober/spanner_prober ./
ENTRYPOINT [ "./spanner_prober" ]
