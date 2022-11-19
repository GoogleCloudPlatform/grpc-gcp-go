package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	proberlib "spanner_prober/prober"

	"cloud.google.com/go/spanner"
	"contrib.go.opencensus.io/exporter/stackdriver"
	"contrib.go.opencensus.io/exporter/stackdriver/monitoredresource"
	log "github.com/golang/glog"
	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/resource"
	"go.opencensus.io/stats/view"
	"google.golang.org/grpc/grpclog"
)

var (
	enableCloudOps  = flag.Bool("enable_cloud_ops", true, "Export metrics to Cloud Operations (former Stackdriver)")
	project         = flag.String("project", "", "GCP project for Cloud Spanner.")
	opsProject      = flag.String("ops_project", "", "Cloud Operations project if differs from Spanner project.")
	instance_name   = flag.String("instance", "test1", "Target instance")
	database_name   = flag.String("database", "test1", "Target database")
	instanceConfig  = flag.String("instance_config", "regional-us-central1", "Target instance config")
	nodeCount       = flag.Int("node_count", 1, "Node count for the prober. If specified, processing_units must be 0.")
	processingUnits = flag.Int("processing_units", 0, "Processing units for the prober. If specified, node_count must be 0.")
	qps             = flag.Float64("qps", 1, "QPS to probe per prober [1, 1000].")
	numRows         = flag.Int("num_rows", 1000, "Number of rows in database to be probed")
	probeType       = flag.String("probe_type", "noop", "The probe type this prober will run")
	maxStaleness    = flag.Duration("max_staleness", 15*time.Second, "Maximum staleness for stale queries")
	payloadSize     = flag.Int("payload_size", 1024, "Size of payload to write to the probe database")
	probeDeadline   = flag.Duration("probe_deadline", 10*time.Second, "Deadline for probe request")
	endpoint        = flag.String("endpoint", "", "Cloud Spanner Endpoint to send request to")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	errs := validateFlags()
	if len(errs) > 0 {
		log.Errorf("Flag validation failed with %v errors", len(errs))
		for _, err := range errs {
			log.Errorf("%v", err)
		}
		log.Exit("Flag validation failed... exiting.")
	}

	grpclog.SetLoggerV2(grpclog.NewLoggerV2(ioutil.Discard, /* Discard logs at INFO level */
		os.Stderr, os.Stderr))

	if *enableCloudOps {
		// Set up the stackdriver exporter for sending metrics.

		// Register gRPC views.
		if err := view.Register(ocgrpc.DefaultClientViews...); err != nil {
			log.Fatalf("Failed to register ocgrpc client views: %v", err)
		}

		// Enable all default views for Cloud Spanner.
		if err := spanner.EnableStatViews(); err != nil {
			log.Errorf("Failed to export stats view: %v", err)
		}

		getPrefix := func(name string) string {
			if strings.HasPrefix(name, proberlib.MetricPrefix) {
				return ""
			}
			return proberlib.MetricPrefix
		}
		exporterOptions := stackdriver.Options{
			ProjectID:            *project,
			BundleDelayThreshold: 60 * time.Second,
			BundleCountThreshold: 3000,
			GetMetricPrefix:      getPrefix,
		}
		if *opsProject != "" {
			exporterOptions.ProjectID = *opsProject
		}
		if os.Getenv(resource.EnvVarType) == "" {
			exporterOptions.MonitoredResource = &MonitoredResource{delegate: monitoredresource.Autodetect()}
		}
		sd, err := stackdriver.NewExporter(exporterOptions)

		if err != nil {
			log.Fatalf("Failed to create the StackDriver exporter: %v", err)
		}
		defer sd.Flush()
		sd.StartMetricsExporter()
		defer sd.StopMetricsExporter()
	}

	prober, err := proberlib.ParseProbeType(*probeType)
	if err != nil {
		log.Exitf("Could not create prober due to %v.", err)
	}

	opts := proberlib.ProberOptions{
		Project:         *project,
		Instance:        *instance_name,
		Database:        *database_name,
		InstanceConfig:  *instanceConfig,
		QPS:             *qps,
		NumRows:         *numRows,
		Prober:          prober,
		MaxStaleness:    *maxStaleness,
		PayloadSize:     *payloadSize,
		ProbeDeadline:   *probeDeadline,
		Endpoint:        *endpoint,
		NodeCount:       *nodeCount,
		ProcessingUnits: *processingUnits,
	}

	p, err := proberlib.NewProber(ctx, opts)
	if err != nil {
		log.Exitf("Failed to initialize the cloud prober, %v", err)
	}
	p.Start(ctx)

	cancelChan := make(chan os.Signal, 1)
	signal.Notify(cancelChan, syscall.SIGTERM, syscall.SIGINT)

	select {
	case <-cancelChan:
	}
}

type MonitoredResource struct {
	monitoredresource.Interface

	delegate monitoredresource.Interface
}

func (mr *MonitoredResource) MonitoredResource() (resType string, labels map[string]string) {
	dType, dLabels := mr.delegate.MonitoredResource()
	resType = dType
	labels = make(map[string]string)
	for k, v := range dLabels {
		if k == "project_id" {
			// Overwrite project id to satisfy Cloud Monitoring rule.
			labels[k] = *project
			if *opsProject != "" {
				labels[k] = *opsProject
			}
			continue
		}
		labels[k] = v
	}
	return
}

func validateFlags() []error {
	var errs []error

	projectRegex, err := regexp.Compile(`^[-_:.a-zA-Z0-9]*$`)
	if err != nil {
		return []error{err}
	}
	instanceDBRegex, err := regexp.Compile(`^[-_.a-zA-Z0-9]*$`)
	if err != nil {
		return []error{err}
	}

	// We limit qps to < 1000 to ensure we don't overload Spanner accidentally.
	if *qps <= 0 || *qps > 1000 {
		errs = append(errs, fmt.Errorf("qps must be 1 <= qps <= 1000, was %v", *qps))
	}

	if *numRows <= 0 {
		errs = append(errs, fmt.Errorf("num_rows must be > 0, was %v", *numRows))
	}

	if *payloadSize <= 0 {
		errs = append(errs, fmt.Errorf("payload_size must be > 0, was %v", *payloadSize))
	}

	if matched := projectRegex.MatchString(*project); !matched {
		errs = append(errs, fmt.Errorf("project did not match %v, was %v", projectRegex, *project))
	}

	if matched := projectRegex.MatchString(*opsProject); !matched {
		errs = append(errs, fmt.Errorf("ops_project did not match %v, was %v", projectRegex, *opsProject))
	}

	if matched := instanceDBRegex.MatchString(*instance_name); !matched {
		errs = append(errs, fmt.Errorf("instance did not match %v, was %v", instanceDBRegex, *instance_name))
	}

	if matched := instanceDBRegex.MatchString(*database_name); !matched {
		errs = append(errs, fmt.Errorf("database did not match %v, was %v", instanceDBRegex, *database_name))
	}

	if matched := instanceDBRegex.MatchString(*instanceConfig); !matched {
		errs = append(errs, fmt.Errorf("instance_config did not match %v, was %v", instanceDBRegex, *instanceConfig))
	}

	if _, err := proberlib.ParseProbeType(*probeType); err != nil {
		errs = append(errs, err)
	}

	return errs
}
