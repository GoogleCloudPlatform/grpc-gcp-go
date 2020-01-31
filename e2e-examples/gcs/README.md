## Example commands

Use grpc API for reading a gcs object through CFE:

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=200mb -method=media -calls=50
```

Use grpc API for reading metadata of a gcs object through CFE:

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=200mb -method=metadata -calls=50
```

Use grpc API for writing a 200mb gcs object through CFE:

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=grpc-write-200mb -method=write -size=204800 -calls=50
```

To use DirectPath, add the arg `-dp` (VM needs to be dp enabled), for example:

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=200mb -calls=50 -dp
```

To use HTTP JSON API, add the arg `-http`, for example:

```sh
go run main.go -bkt=gcs-grpc-team-weiranf -obj=200mb -method=media -calls=50 -http
```
