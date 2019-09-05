# End to End Checksum Client

This is an example datastore client applying end to end checksum for data
integrity.

## Usage

For this client, you can choose to use the production datastore target
("datastore.googleapis.com"), or use a test server running behind GFE.

## Run client

Override datastore endpoint.

```sh
export DATASTORE_EMULATOR_HOST=my-test-service.sandbox.googleapis.com:443
```

Run:

```sh
go run main.go
```

