package main

import (
	"reflect"
	proberlib "spanner_prober/prober"
	"testing"
	"time"
)

func TestValidFlags(t *testing.T) {
	var tests = []struct {
		name           string
		project        string
		instance       string
		database       string
		instanceConfig string
		qps            float64
		numRows        int
		probeType      string
		maxStaleness   time.Duration
		payloadSize    int
		expectedErrors int
	}{
		{
			name:           "good flags",
			project:        "google.com:abc",
			instance:       "abc",
			database:       "abc",
			instanceConfig: "regional-test-1",
			qps:            1,
			numRows:        1,
			probeType:      "noop",
			maxStaleness:   5 * time.Second,
			payloadSize:    1024,
			expectedErrors: 0,
		},
		{
			name:           "uris not names",
			project:        "projects/google.com:abc",
			instance:       "projects/google.com:abc/instances/test-instance",
			database:       "projects/google.com:abc/instances/test-instance/databases/test-database",
			instanceConfig: "projects/google.com:abc/instanceConfigs/regional-test-instance",
			qps:            1,
			numRows:        1,
			probeType:      "noop",
			payloadSize:    1,
			expectedErrors: 4,
		},
		{
			name:           "invalid options",
			project:        "+abc",
			instance:       "abc!",
			database:       "abc=",
			instanceConfig: "<abc>",
			qps:            0,
			numRows:        0,
			probeType:      "notaprobe",
			payloadSize:    -1,
			expectedErrors: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*project = tt.project
			*instance_name = tt.instance
			*database_name = tt.database
			*instanceConfig = tt.instanceConfig
			*qps = tt.qps
			*numRows = tt.numRows
			*probeType = tt.probeType
			*maxStaleness = tt.maxStaleness
			*payloadSize = tt.payloadSize

			errs := validateFlags()
			if len(errs) != tt.expectedErrors {
				t.Errorf("validateFlags() got %v errors, want %v errors: %q", len(errs), tt.expectedErrors, errs)
			}
		})
	}
}

func TestParseProbeType(t *testing.T) {
	var tests = []struct {
		name      string
		flag      string
		probeType proberlib.Probe
		wantErr   bool
	}{
		{
			name:      "good type",
			flag:      "stale_read",
			probeType: proberlib.StaleReadProbe{},
		},
		{
			name:      "bad type",
			flag:      "not_a_probe",
			probeType: proberlib.NoopProbe{},
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := proberlib.ParseProbeType(tt.flag)

			if (err != nil) != tt.wantErr {
				t.Errorf("parseProbeType(%q) = error %v, wantErr %t", tt.flag, err, tt.wantErr)
			}
			if got, want := reflect.TypeOf(result), reflect.TypeOf(tt.probeType); got != want {
				t.Errorf("parseProbeType(%q) = %v, want %v", tt.flag, got, want)
			}
		})
	}
}
