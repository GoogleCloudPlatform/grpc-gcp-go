# gRPC-GCP Go Spanner prober

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
