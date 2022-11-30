# gRPC-GCP Go Spanner prober

The prober performs an operation (set by `probe_type`) at a stable rate (`qps`).

All operations access the "ProbeTarget" table in the `database` in the `instance`.

The prober will try to create the instance, database, and the table if any of them doesn't exist.

For each operation, the prober picks a random number from 0 to `num_rows` and performs read and/or write operations with the selected number as the primary key.

Each write call updates/inserts three columns: a primary key, a `payload_size`-bytes randomly generated payload, and a SHA256 checksum of the payload.

## Usage

Build image:

    docker build -t spanner_prober:latest .

Run prober:

    docker run --rm -v <path-to-gcp-credentials-json>:/gcp-creds.json --env GOOGLE_APPLICATION_CREDENTIALS=/gcp-creds.json spanner_prober:latest --project=<gcp-project> --qps=0.5 --probe_type=read_write

## Arguments

- enable_cloud_ops - Export metrics to Cloud Operations (former Stackdriver). (Default: true)
- project - GCP project for Cloud Spanner.
- ops_project - Cloud Operations project if differs from Spanner project.
- instance - Target instance. (Default: "test1")
- database - Target database. (Default: "test1")
- instance_config - Target instance config (Default: "regional-us-central1")
- node_count - Node count for the prober. If specified, processing_units must be 0. (Default: 1)
- processing_units - Processing units for the prober. If specified, node_count must be 0. (Default: 0)
- qps - QPS to probe per prober [1, 1000]. (Default: 1)
- num_rows - Number of rows in database to be probed. (Default: 1000)
- probe_type - The probe type this prober will run. (Default: "noop")
- max_staleness - Maximum staleness for stale queries. (Default: 15s)
- payload_size - Size of payload to write to the probe database. (Default: 1024)
- probe_deadline - Deadline for probe request. (Default: 10s)
- endpoint - Cloud Spanner Endpoint to send request to.

## Disabling automatic resource detection

If running on GCE/GKE the metrics will be shipped to corresponding "VM Instance"/"Kubernetes Container" resource in Cloud Monitoring. To disable this and use global resource set environment variable `OC_RESOURCE_TYPE=global`.

## Probe types

| probe_type   | Description                                                    |
| ------------ | -------------------------------------------------------------- |
| noop         | no operation.                                                  |
| stale_read   | read-only txn `Read` with timestamp bound.                     |
| strong_query | read-only txn `Query`.                                         |
| stale_query  | read-only txn `Query` with timestamp bound.                    |
| dml          | read-write txn with `Query` followed by `Update`.              |
| read_write   | read-write txn with `Read` followed by `BufferWrite` mutation. |

## Reported metrics

All metrics are prefixed with `custom.googleapis.com/opencensus/grpc_gcp_spanner_prober/`

`op_name` label 

| Metric       | Unit | Kind       | Value        | Decription
| ------------ | ---- | ---------- | ------------ | ----------
| **Prober specific**
| op_count     | 1    | Cumulative | Int64        | Operation count. Labeled by: op_name, result.
| op_latency   | ms   | Cumulative | Distribution | Operation latency. Labeled by: op_name, result.
| t4t7_latency | ms   | Cumulative | Distribution | GFE latency. Labeled by: rpc_type (unary/streaming), grpc_client_method
| **Opencensus default gRPC metrics** | | | | additional prefix `grpc.io/client/`
| completed_rpcs         | 1    | Cumulative | Int64        | Count of RPCs by method and status.
| received_bytes_per_rpc | byte | Cumulative | Distribution | Distribution of bytes received per RPC, by method.
| roundtrip_latency      | ms   | Cumulative | Distribution | Distribution of round-trip latency, by method.
| sent_bytes_per_rpc     | byte | Cumulative | Distribution | Distribution of bytes sent per RPC, by method.
| **From Spanner client** | | | | additional prefix `cloud.google.com/go/spanner/` Labels: client_id, instance_id, database, library_version
| max_allowed_sessions  | 1 | Gauge      | Int64 | The maximum number of sessions allowed. Configurable by the user.
| max_in_use_sessions   | 1 | Gauge      | Int64 | The maximum number of sessions in use during the last 10 minute interval.
| num_acquired_sessions | 1 | Cumulative | Int64 | The number of sessions acquired from the session pool.
| num_released_sessions | 1 | Cumulative | Int64 | The number of sessions released by the user and pool maintainer.
| num_sessions_in_pool  | 1 | Gauge      | Int64 | The number of sessions currently in use. Labeled by type.
| open_session_count    | 1 | Gauge      | Int64 | Number of sessions currently opened.
