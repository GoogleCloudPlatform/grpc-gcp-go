## Example commands

Use grpc API for reading 200mb gcs object through CFE:

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=200mb -calls=50
```

Use grpc API for reading 200mb gcs object through DirectPath (VM needs to be dp
enabled):

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=200mb -calls=50 -dp
```

Use HTTP JSON API for reading 200mb gcs object through CFE:

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=200mb -calls=50 -http
```
