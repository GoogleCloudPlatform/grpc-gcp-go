// Package prober defines a Cloud Spanner prober.
package prober

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	instance "cloud.google.com/go/spanner/admin/instance/apiv1"
	log "github.com/golang/glog"
	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dbadminpb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	instancepb "google.golang.org/genproto/googleapis/spanner/admin/instance/v1"
)

const (
	aggregationChannelSize = 1000
	// Long running operations retry parameters.
	baseLRORetryDelay = 200 * time.Millisecond
	maxLRORetryDelay  = 5 * time.Second
)

var (
	generatePayload = func(size int) ([]byte, []byte, error) {
		payload := make([]byte, size)
		rand.Read(payload)
		h := sha256.New()
		if _, err := h.Write(payload); err != nil {
			return nil, nil, err
		}
		return payload, h.Sum(nil), nil
	}

	MetricPrefix = "grpc_gcp_spanner_prober/"

	opNameTag = tag.MustNewKey("op_name")

	resultTag   = tag.MustNewKey("result")
	withError   = tag.Insert(resultTag, "error")
	withSuccess = tag.Insert(resultTag, "success")

	opLatency = stats.Int64(
		"op_latency",
		"gRPC-GCP Spanner prober operation latency",
		stats.UnitMilliseconds,
	)
	opLatencyView = &view.View{
		Name:        MetricPrefix + opLatency.Name(),
		Measure:     opLatency,
		Aggregation: view.Distribution(expDistribution...),
		TagKeys:     []tag.Key{resultTag, opNameTag},
	}

	opResults = stats.Int64(
		"op_count",
		"gRPC-GCP Spanner prober operation count",
		stats.UnitDimensionless,
	)
	opResultsView = &view.View{
		Name:        MetricPrefix + opResults.Name(),
		Measure:     opResults,
		Aggregation: view.Count(),
		TagKeys:     []tag.Key{resultTag, opNameTag},
	}
)

// Options holds the settings required for creating a prober.
type ProberOptions struct {

	// Cloud Spanner settings in name form, not URI (e.g. an_instance, not projects/a_project/instances/an_instance).
	Project        string
	Instance       string
	Database       string
	InstanceConfig string

	// QPS rate to probe at.
	QPS float64

	// NumRows is the number of rows in which which the prober randomly chooses to probe.
	NumRows int

	// Prober is type of probe which will be run.
	Prober Probe

	// MaxStaleness is the bound of stale reads.
	MaxStaleness time.Duration

	// PayloadSize is the number of bytes of random data used as a Payload.
	PayloadSize int

	// ProbeDeadline is the deadline for request for probes.
	ProbeDeadline time.Duration

	// Cloud Spanner Endpoint to send request.
	Endpoint string

	// Node count for Spanner instance. If specified, ProcessingUnits must be 0.
	NodeCount int

	// Processing units for the Spanner instance. If specified, NodeCount must be 0.
	ProcessingUnits int
}

// Prober holds the internal prober state.
type Prober struct {
	instanceAdminClient *instance.InstanceAdminClient
	spannerClient       *spanner.Client
	deadline            time.Duration
	mu                  *sync.Mutex
	opsChannel          chan op
	numRows             int
	qps                 float64
	prober              Probe
	maxStaleness        time.Duration
	payloadSize         int
	generatePayload     func(int) ([]byte, []byte, error)
	opt                 ProberOptions
	// add clientOptions here as it contains credentials.
	clientOpts []option.ClientOption
}

type op struct {
	Latency time.Duration
	Error   error
}

// Probe is the interface for a single type of prober.
type Probe interface {
	name() string
	probe(context.Context, *Prober) error
}

// instanceURI returns an instance URI of the form: projects/{project}/instances/{instance name}.
// E.g. projects/test-project/instances/test-instance.
func (opt *ProberOptions) instanceURI() string {
	return fmt.Sprintf("projects/%s/instances/%s", opt.Project, opt.Instance)
}

// instanceName returns an instance name of the form: {instance name}.
// E.g. test-instance.
func (opt *ProberOptions) instanceName() string {
	return opt.Instance
}

// instanceConfigURI returns an instance config URI of the form: projects/{project}/instanceConfigs/{instance config name}.
// E.g. projects/test-project/instanceConfigss/regional-test.
func (opt *ProberOptions) instanceConfigURI() string {
	return fmt.Sprintf("projects/%s/instanceConfigs/%s", opt.Project, opt.InstanceConfig)
}

// projectURI returns a project URI of the form: projects/{project}.
// E.g. projects/test-project.
func (opt *ProberOptions) projectURI() string {
	return fmt.Sprintf("projects/%s", opt.Project)
}

// databaseURI returns a database URI of the form: projects/{project}/instances/{instance name}/databases/{database name}.
// E.g. projects/test-project/instances/test-instance/databases/test-database.
func (opt *ProberOptions) databaseURI() string {
	return fmt.Sprintf("projects/%s/instances/%s/databases/%s", opt.Project, opt.Instance, opt.Database)
}

// databaseName returns a database name of the form: {database name}.
// E.g. test-database.
func (opt *ProberOptions) databaseName() string {
	return opt.Database
}

func init() {
	view.Register(opLatencyView, opResultsView)
}

// New initializes Cloud Spanner clients, setup up the database, and return a new CSProber.
func NewProber(ctx context.Context, opt ProberOptions, clientOpts ...option.ClientOption) (*Prober, error) {
	ctx, err := tag.New(ctx, tag.Insert(opNameTag, opt.Prober.name()))
	if err != nil {
		return nil, err
	}
	// Override Cloud Spanner endpoint if specified.
	if opt.Endpoint != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(opt.Endpoint))
	}
	p, err := newSpannerProber(ctx, opt, clientOpts...)
	if err != nil {
		return nil, err
	}

	go p.backgroundStatsAggregator(ctx)

	return p, nil
}

func newSpannerProber(ctx context.Context, opt ProberOptions, clientOpts ...option.ClientOption) (*Prober, error) {
	if opt.NumRows <= 0 {
		return nil, fmt.Errorf("NumRows must be at least 1, got %v", opt.NumRows)
	}

	if opt.NodeCount > 0 && opt.ProcessingUnits > 0 {
		return nil, fmt.Errorf("At most one of NodeCount or ProcessingUnits may be specified. NodeCount: %v, ProcessingUnits: %v", opt.NodeCount, opt.ProcessingUnits)
	}

	if opt.Prober == nil {
		return nil, errors.New("Prober must not be nil")
	}

	instanceClient, err := instance.NewInstanceAdminClient(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}

	if err := createCloudSpannerInstanceIfMissing(ctx, instanceClient, opt); err != nil {
		return nil, err
	}

	databaseClient, err := database.NewDatabaseAdminClient(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	defer databaseClient.Close()
	if err := createCloudSpannerDatabase(ctx, databaseClient, opt); err != nil {
		return nil, err
	}

	clientOpts = append(
		clientOpts,
		option.WithGRPCDialOption(grpc.WithUnaryInterceptor(AddGFELatencyUnaryInterceptor)),
		option.WithGRPCDialOption(grpc.WithStreamInterceptor(AddGFELatencyStreamingInterceptor)),
		option.WithGRPCDialOption(grpc.WithStatsHandler(new(ocgrpc.ClientHandler))),
	)
	dataClient, err := spanner.NewClient(ctx, opt.databaseURI(), clientOpts...)
	if err != nil {
		return nil, err
	}

	p := &Prober{
		instanceAdminClient: instanceClient,
		spannerClient:       dataClient,
		deadline:            time.Duration(opt.ProbeDeadline),
		opsChannel:          make(chan op, aggregationChannelSize),
		numRows:             opt.NumRows,
		qps:                 opt.QPS,
		prober:              opt.Prober,
		maxStaleness:        opt.MaxStaleness,
		payloadSize:         opt.PayloadSize,
		generatePayload:     generatePayload,
		mu:                  &sync.Mutex{},
		opt:                 opt,
		clientOpts:          clientOpts,
	}

	return p, nil
}

func backoff(baseDelay, maxDelay time.Duration, retries int) time.Duration {
	backoff, max := float64(baseDelay), float64(maxDelay)
	for backoff < max && retries > 0 {
		backoff = backoff * 1.5
		retries--
	}
	if backoff > max {
		backoff = max
	}
	return time.Duration(backoff)
}

// createCloudSpannerInstanceIfMissing creates a one node "Instance" of Cloud Spanner in the specificed project if missing.
// Instances are shared across multiple prober tasks running in differenct GCE VMs.
// Instances do not get cleaned up in this prober, as we do not support turning down Cloud Spanner regions.
func createCloudSpannerInstanceIfMissing(ctx context.Context, instanceClient *instance.InstanceAdminClient, opt ProberOptions) error {
	// Skip instance creation requests if the instance already exists.
	if checkInstancePresence(ctx, instanceClient, opt) {
		return nil
	}

	op, err := instanceClient.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
		Parent:     opt.projectURI(),
		InstanceId: opt.instanceName(),
		Instance: &instancepb.Instance{
			Name:            opt.instanceURI(),
			Config:          opt.instanceConfigURI(),
			DisplayName:     opt.instanceName(),
			NodeCount:       int32(opt.NodeCount),
			ProcessingUnits: int32(opt.ProcessingUnits),
		},
	})

	// If instance create operations fails, check if the instance was already created. If no, return error.
	if err != nil {
		if status.Code(err) != codes.AlreadyExists {
			return err
		}
	} else if _, err := op.Wait(ctx); err != nil {
		return err
	}

	// Wait for instance to be ready.
	var retries int
	for {
		var resp *instancepb.Instance
		resp, err = instanceClient.GetInstance(ctx, &instancepb.GetInstanceRequest{
			Name: opt.instanceURI(),
		})
		if err != nil || resp.State == instancepb.Instance_READY {
			return err
		}
		select {
		case <-time.After(backoff(baseLRORetryDelay, maxLRORetryDelay, retries)):
		case <-ctx.Done():
			return ctx.Err()
		}
		retries++
	}
}

// checkInstancePresence checks whether the instance is already created.
func checkInstancePresence(ctx context.Context, instanceClient *instance.InstanceAdminClient, opt ProberOptions) bool {
	resp, err := instanceClient.GetInstance(ctx, &instancepb.GetInstanceRequest{
		Name: opt.instanceURI(),
	})
	// If instance is not present or instance is not ready.
	if err != nil || resp.State != instancepb.Instance_READY {
		return false
	}
	return true
}

// createCloudSpannerDatabase creates a prober "Database" on an "Instance" of Cloud Spanner in the specificed project.
// Databases are shared across multiple prober tasks running in differenct GCE VMs.
// Databases do not get cleaned up in this prober, as we do not support turning down Cloud Spanner regions.
func createCloudSpannerDatabase(ctx context.Context, databaseClient *database.DatabaseAdminClient, opt ProberOptions) error {
	op, err := databaseClient.CreateDatabase(ctx, &dbadminpb.CreateDatabaseRequest{
		Parent:          opt.instanceURI(),
		CreateStatement: fmt.Sprintf("CREATE DATABASE `%v`", opt.databaseName()),
		ExtraStatements: []string{
			`CREATE TABLE ProbeTarget (
					Id INT64 NOT NULL,
					Payload BYTES(MAX),
					PayloadHash BYTES(MAX),
				) PRIMARY KEY (Id)`,
		},
	})

	if err != nil {
		if code := status.Code(err); code == codes.AlreadyExists {
			return nil
		}
		return err
	}

	_, err = op.Wait(ctx)
	return err
}

// backgroundStatsAggregator pulls stats from the prober channel.
// This avoids the case in which probes are blocked due to the channel being full.
func (p *Prober) backgroundStatsAggregator(ctx context.Context) error {
	for op := range p.opsChannel {
		mutator := withSuccess
		if op.Error != nil {
			mutator = withError
		}
		stats.RecordWithTags(ctx, []tag.Mutator{mutator}, opResults.M(1))
		stats.RecordWithTags(ctx, []tag.Mutator{mutator}, opLatency.M(op.Latency.Milliseconds()))
	}

	return nil
}

func (p *Prober) probeInterval() time.Duration {
	// qps must be > 0 as we validate this when constructing a CSProber.
	// qps is converted to a duration for type reasons, however it does not represent a value duration.
	// Use granularity of nanosecond to support float division. Useful for supporting probes with QPS < 1.
	return time.Duration(float64(time.Second) / p.qps)
}

// Start starts the prober. This will run a goroutinue until ctx is canceled.
func (p *Prober) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(p.probeInterval())

		for {
			select {
			case <-ctx.Done():
				// Stop probing when the context is canceled.
				log.Info("Probing stopped as context is done.")
				return
			case <-ticker.C:
				go func() {
					probeCtx, cancel := context.WithTimeout(ctx, p.deadline)
					defer cancel()
					p.runProbe(probeCtx)
				}()
			}
		}
	}()
}

func (p *Prober) runProbe(ctx context.Context) {
	startTime := time.Now()
	probeErr := p.prober.probe(ctx, p)
	latency := time.Now().Sub(startTime)

	p.opsChannel <- op{
		Latency: latency,
		Error:   probeErr,
	}
}

// validateRows checks that the read did not return an error, and if the row is
// present it validates the hash. If the row is not present it completes successfully.
// Returns the number of validated rows, and the first error encounted.
func validateRows(iter *spanner.RowIterator) (int, error) {
	rows := 0
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			return rows, nil
		}
		if err != nil {
			return rows, err
		}

		var id int64
		var payload, payloadHash []byte
		if err := row.Columns(&id, &payload, &payloadHash); err != nil {
			return rows, err
		}

		h := sha256.New()
		_, err = h.Write(payload)
		if err != nil {
			return rows, err
		}
		if calculatedHash := h.Sum(nil); !bytes.Equal(calculatedHash, payloadHash) {
			return rows, fmt.Errorf("hash for row %v did not match, got %v, want %v", id, calculatedHash, payloadHash)
		}

		rows++
	}
}

func ParseProbeType(t string) (Probe, error) {
	switch t {
	case "noop":
		return NoopProbe{}, nil
	case "stale_read":
		return StaleReadProbe{}, nil
	case "strong_query":
		return StrongQueryProbe{}, nil
	case "stale_query":
		return StaleQueryProbe{}, nil
	case "dml":
		return DMLProbe{}, nil
	case "read_write":
		return ReadWriteProbe{}, nil
	default:
		return NoopProbe{}, fmt.Errorf("probe_type %q is not a valid probe type", t)
	}
}

// NoopProbe is a fake prober which always succeeds.
type NoopProbe struct{}

func (NoopProbe) name() string { return "noop" }

func (NoopProbe) probe(ctx context.Context, p *Prober) error {
	return nil
}

// StaleReadProbe performs a stale read of the database.
type StaleReadProbe struct{}

func (StaleReadProbe) name() string { return "stale_read" }

func (StaleReadProbe) probe(ctx context.Context, p *Prober) error {
	// Random row within the range.
	k := rand.Intn(p.numRows)

	txn := p.spannerClient.Single().WithTimestampBound(spanner.MaxStaleness(p.maxStaleness))
	iter := txn.Read(ctx, "ProbeTarget", spanner.Key{k}, []string{"Id", "Payload", "PayloadHash"})
	defer iter.Stop()
	_, err := validateRows(iter)
	return err
}

// StrongQueryProbe performs a strong query of the database.
type StrongQueryProbe struct{}

func (StrongQueryProbe) name() string { return "strong_query" }

func (StrongQueryProbe) probe(ctx context.Context, p *Prober) error {
	// Random row within the range.
	k := rand.Intn(p.numRows)

	stmt := spanner.Statement{
		SQL: `select t.Id, t.Payload, t.PayloadHash from ProbeTarget t where t.Id = @Id`,
		Params: map[string]interface{}{
			"Id": k,
		},
	}
	iter := p.spannerClient.Single().Query(ctx, stmt)
	defer iter.Stop()
	_, err := validateRows(iter)
	return err
}

// StaleQueryProbe performs a stale query of the database.
type StaleQueryProbe struct{}

func (StaleQueryProbe) name() string { return "stale_query" }

func (StaleQueryProbe) probe(ctx context.Context, p *Prober) error {
	// Random row within the range.
	k := rand.Intn(p.numRows)

	stmt := spanner.Statement{
		SQL: `select t.Id, t.Payload, t.PayloadHash from ProbeTarget t where t.Id = @Id`,
		Params: map[string]interface{}{
			"Id": k,
		},
	}
	iter := p.spannerClient.Single().WithTimestampBound(spanner.MaxStaleness(p.maxStaleness)).Query(ctx, stmt)
	defer iter.Stop()
	_, err := validateRows(iter)
	return err
}

// DMLProbe performs a SQL based transaction in the database.
type DMLProbe struct{}

func (DMLProbe) name() string { return "dml" }

func (DMLProbe) probe(ctx context.Context, p *Prober) error {
	// Random row within the range.
	k := rand.Intn(p.numRows)

	payload, payloadHash, err := p.generatePayload(p.payloadSize)
	if err != nil {
		return err
	}

	_, err = p.spannerClient.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		readStmt := spanner.Statement{
			SQL: `select t.Id, t.Payload, t.PayloadHash from ProbeTarget t where t.Id = @Id`,
			Params: map[string]interface{}{
				"Id": k,
			},
		}
		iter := txn.Query(ctx, readStmt)
		defer iter.Stop()
		rows, err := validateRows(iter)
		if err != nil {
			return err
		}

		// Update the row with a new random value if the row already exisis, otherwise insert a new row.
		dmlSQL := `update ProbeTarget t set t.Payload = @payload, t.PayloadHash = @payloadHash where t.Id = @Id`
		if rows == 0 {
			dmlSQL = `insert ProbeTarget (Id, Payload, PayloadHash) VALUES(@Id, @payload, @payloadHash)`
		}
		dmlStmt := spanner.Statement{
			SQL: dmlSQL,
			Params: map[string]interface{}{
				"Id":          k,
				"payload":     payload,
				"payloadHash": payloadHash,
			},
		}
		_, err = txn.Update(ctx, dmlStmt)
		return err
	})

	return err
}

// ReadWriteProbe performs a mutation based transaction in the database.
type ReadWriteProbe struct{}

func (ReadWriteProbe) name() string { return "read_write" }

func (ReadWriteProbe) probe(ctx context.Context, p *Prober) error {
	// Random row within the range.
	k := rand.Intn(p.numRows)

	payload, payloadHash, err := p.generatePayload(p.payloadSize)
	if err != nil {
		return err
	}

	_, err = p.spannerClient.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		iter := txn.Read(ctx, "ProbeTarget", spanner.Key{k}, []string{"Id", "Payload", "PayloadHash"})
		defer iter.Stop()
		_, err := validateRows(iter)
		if err != nil {
			return err
		}

		return txn.BufferWrite([]*spanner.Mutation{
			spanner.InsertOrUpdate("ProbeTarget", []string{"Id", "Payload", "PayloadHash"}, []interface{}{k, payload, payloadHash}),
		})
	})

	return err
}
