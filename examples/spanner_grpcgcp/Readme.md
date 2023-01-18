### Spanner client using gRPC-GCP channel pool example

You'll need a Spanner database to test connectivity via gRPC-GCP channel pool.

Run

```
go run spanner_grpcgcp.go --project=your-gcp-project --instance=your-spanner-instance --database=your-spanner-database
```

The output should look like:

```
2023/01/18 19:28:10 Returned: 1
```
