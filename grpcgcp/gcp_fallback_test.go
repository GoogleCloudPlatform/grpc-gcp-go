/*
 *
 * Copyright 2025 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package grpcgcp

import (
	"context"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/mocks"
	"github.com/golang/mock/gomock"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockCtrl struct {
	*gomock.Controller
}

func setup(t *testing.T, opts *GCPFallbackOptions) (*mockCtrl, *GCPFallback, *mocks.MockClientConnInterface, *mocks.MockClientConnInterface) {
	t.Helper()
	ctrl := gomock.NewController(t)
	primaryConn := mocks.NewMockClientConnInterface(ctrl)
	fallbackConn := mocks.NewMockClientConnInterface(ctrl)

	if opts == nil {
		opts = NewGCPFallbackOptions()
		opts.Period = 10 * time.Second
		opts.ErrorRateThreshold = 0.5
		opts.MinFailedCalls = 3
	}

	gcpFallback, err := NewGCPFallback(context.Background(), primaryConn, fallbackConn, opts)
	if err != nil {
		t.Fatalf("NewGCPFallback() error = %v", err)
	}
	t.Cleanup(gcpFallback.Close)

	return &mockCtrl{ctrl}, gcpFallback, primaryConn, fallbackConn
}

// Expect a unary call to be made on a connection and make this call return the error code.
func expectUnary(t *testing.T, conn *mocks.MockClientConnInterface, code codes.Code) *gomock.Call {
	t.Helper()

	if code == codes.OK {
		return conn.EXPECT().Invoke(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil)
	}

	return conn.EXPECT().Invoke(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(status.Error(code, code.String()))
}

// Expect a streaming call to be made on a connection and make this call return the error code.
func (mc *mockCtrl) expectStreaming(t *testing.T, conn *mocks.MockClientConnInterface, code codes.Code) *gomock.Call {
	t.Helper()

	if code == codes.OK {
		return conn.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
				mockStream := mocks.NewMockClientStream(mc.Controller)
				mockStream.EXPECT().RecvMsg(gomock.Any()).Return(io.EOF)
				return mockStream, nil
			})
	}

	return conn.EXPECT().NewStream(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			mockStream := mocks.NewMockClientStream(mc.Controller)
			mockStream.EXPECT().RecvMsg(gomock.Any()).Return(status.Error(code, code.String()))
			return mockStream, nil
		})
}

func TestGCPFallback_FallbackHappens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl, gcpFallback, primaryConn, fallbackConn := setup(t, nil)

		// Prepare 3 failures, 2 successes. Rate = 3/5 = 0.6 >= 0.5. Failures = 3 >= 3.
		// Fallback should be triggered.
		expectUnary(t, primaryConn, codes.Unavailable).Times(2)
		ctrl.expectStreaming(t, primaryConn, codes.Unavailable).Times(1)
		expectUnary(t, primaryConn, codes.OK).Times(1)
		ctrl.expectStreaming(t, primaryConn, codes.OK).Times(1)

		for i := 0; i < 3; i++ {
			gcpFallback.Invoke(context.Background(), "method", nil, nil)
		}
		for i := 0; i < 2; i++ {
			s, err := gcpFallback.NewStream(context.Background(), nil, "method")
			if err != nil {
				t.Fatalf("NewStream() error = %v", err)
			}
			s.RecvMsg(nil) // This will trigger the result.
		}

		// Wait for rate check to trigger fallback.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		expectUnary(t, fallbackConn, codes.OK)
		if err := gcpFallback.Invoke(context.Background(), "method", nil, nil); err != nil {
			t.Errorf("Invoke() on gcpFallback failed: %v", err)
		}
	})
}

func TestGCPFallback_NoFallback_Disabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := NewGCPFallbackOptions()
		opts.Period = 10 * time.Second
		opts.ErrorRateThreshold = 0.5
		opts.MinFailedCalls = 3
		opts.EnableFallback = false
		_, gcpFallback, primaryConn, _ := setup(t, opts)

		// 3 failures, 2 successes. Rate = 0.6. Conditions met, but fallback is disabled.
		expectUnary(t, primaryConn, codes.Unavailable).Times(3)
		expectUnary(t, primaryConn, codes.OK).Times(2)

		for i := 0; i < 5; i++ {
			gcpFallback.Invoke(context.Background(), "method", nil, nil)
		}

		// Wait for rate check.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		// Should still use primary.
		expectUnary(t, primaryConn, codes.OK).Times(1)
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	})
}

func TestGCPFallback_NoFallback_LowRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl, gcpFallback, primaryConn, _ := setup(t, nil)

		// 2 failures, 3 successes. Rate = 2/5 = 0.4 < 0.5.
		expectUnary(t, primaryConn, codes.Unavailable).Times(1)
		ctrl.expectStreaming(t, primaryConn, codes.Unavailable).Times(1)
		expectUnary(t, primaryConn, codes.OK).Times(2)
		ctrl.expectStreaming(t, primaryConn, codes.OK).Times(1)

		for i := 0; i < 3; i++ {
			gcpFallback.Invoke(context.Background(), "method", nil, nil)
		}
		for i := 0; i < 2; i++ {
			s, err := gcpFallback.NewStream(context.Background(), nil, "method")
			if err != nil {
				t.Fatalf("NewStream() error = %v", err)
			}
			s.RecvMsg(nil) // This will trigger the result.
		}

		// Wait for rate check.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		// No fallbck because error rate is lower. Should still use primary.
		expectUnary(t, primaryConn, codes.OK).Times(1)
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	})
}

func TestGCPFallback_NoFallback_TooFewFailedCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, gcpFallback, primaryConn, _ := setup(t, nil)

		// 2 failures, 0 successes. Rate = 1.0, but failures = 2 < 3.
		expectUnary(t, primaryConn, codes.Unavailable).Times(2)

		for i := 0; i < 2; i++ {
			gcpFallback.Invoke(context.Background(), "method", nil, nil)
		}

		// Wait for rate check.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		// Should still use primary.
		expectUnary(t, primaryConn, codes.Unavailable).Times(1)
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	})
}

func TestGCPFallback_NoFallback_DifferentErrorCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl, gcpFallback, primaryConn, _ := setup(t, nil)

		// 6 failures, but with canceled error code.
		expectUnary(t, primaryConn, codes.Canceled).Times(3)
		ctrl.expectStreaming(t, primaryConn, codes.Canceled).Times(3)

		for i := 0; i < 3; i++ {
			gcpFallback.Invoke(context.Background(), "method", nil, nil)
		}
		for i := 0; i < 3; i++ {
			s, err := gcpFallback.NewStream(context.Background(), nil, "method")
			if err != nil {
				t.Fatalf("NewStream() error = %v", err)
			}
			s.RecvMsg(nil) // This will trigger the result.
		}

		// Wait for rate check.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		// Should still use primary.
		expectUnary(t, primaryConn, codes.OK).Times(1)
		gcpFallback.Invoke(context.Background(), "method", nil, nil)
	})
}

func metricsDataFromReader(ctx context.Context, t *testing.T, reader *metric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	rm := &metricdata.ResourceMetrics{}
	if err := reader.Collect(ctx, rm); err != nil {
		t.Fatalf("reader.Collect() error = %v", err)
	}
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}
	return gotMetrics
}

func TestGCPFallback_Metrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader := metric.NewManualReader()
		provider := metric.NewMeterProvider(metric.WithReader(reader))

		opts := NewGCPFallbackOptions()
		opts.Period = 10 * time.Second
		opts.ErrorRateThreshold = 0.5
		opts.MinFailedCalls = 2
		opts.MeterProvider = provider

		ctrl, gcpFallback, primaryConn, fallbackConn := setup(t, opts)

		// 2 failures, 1 success. Rate = 2/3 = 0.66 >= 0.5. Failures = 2 >= 2.
		// Fallback should be triggered.
		expectUnary(t, primaryConn, codes.Unavailable).Times(1)
		ctrl.expectStreaming(t, primaryConn, codes.Unavailable).Times(1)
		expectUnary(t, primaryConn, codes.OK).Times(1)

		gcpFallback.Invoke(context.Background(), "method", nil, nil)
		s, err := gcpFallback.NewStream(context.Background(), nil, "method")
		if err != nil {
			t.Fatalf("NewStream() error = %v", err)
		}
		s.RecvMsg(nil) // This will trigger the result.
		gcpFallback.Invoke(context.Background(), "method", nil, nil)

		wantMetrics := []metricdata.Metrics{
			{
				Name:        "eef.call_status",
				Description: "Number of calls with a status and channel.",
				Unit:        "{call}",
				Data: metricdata.Sum[int64]{
					DataPoints: []metricdata.DataPoint[int64]{
						{Value: 2, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"), attribute.String("status_code", "Unavailable"))},
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"), attribute.String("status_code", "OK"))},
					},
					IsMonotonic: true,
					Temporality: metricdata.CumulativeTemporality,
				},
			},
			{
				Name:        "eef.current_channel",
				Description: "1 for currently active channel, 0 otherwise.",
				Unit:        "{channel}",
				Data: metricdata.Sum[int64]{
					DataPoints: []metricdata.DataPoint[int64]{
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"))},
						{Value: 0, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"))},
					},
					IsMonotonic: false,
					Temporality: metricdata.CumulativeTemporality,
				},
			},
		}

		dontWantMetrics := []string{
			"eef.fallback_count",
			"eef.error_ratio",
		}

		// Read metrics before fallback.
		gotMetrics := metricsDataFromReader(context.Background(), t, reader)
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}
		for _, metricName := range dontWantMetrics {
			if _, ok := gotMetrics[metricName]; ok {
				t.Fatalf("Metric %v should not be present in recorded metrics", metricName)
			}
		}

		// Wait for rate check to trigger fallback.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		// Fallback should be used.
		expectUnary(t, fallbackConn, codes.OK)
		if err := gcpFallback.Invoke(context.Background(), "method", nil, nil); err != nil {
			t.Errorf("Invoke() on gcpFallback failed: %v", err)
		}

		wantMetrics = []metricdata.Metrics{
			{
				Name:        "eef.call_status",
				Description: "Number of calls with a status and channel.",
				Unit:        "{call}",
				Data: metricdata.Sum[int64]{
					DataPoints: []metricdata.DataPoint[int64]{
						{Value: 2, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"), attribute.String("status_code", "Unavailable"))},
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"), attribute.String("status_code", "OK"))},
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"), attribute.String("status_code", "OK"))},
					},
					IsMonotonic: true,
					Temporality: metricdata.CumulativeTemporality,
				},
			},
			{
				Name:        "eef.current_channel",
				Description: "1 for currently active channel, 0 otherwise.",
				Unit:        "{channel}",
				Data: metricdata.Sum[int64]{
					DataPoints: []metricdata.DataPoint[int64]{
						{Value: 0, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"))},
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"))},
					},
					IsMonotonic: false,
					Temporality: metricdata.CumulativeTemporality,
				},
			},
			{
				Name:        "eef.fallback_count",
				Description: "Number of fallbacks occurred from one channel to another.",
				Unit:        "{occurrence}",
				Data: metricdata.Sum[int64]{
					DataPoints: []metricdata.DataPoint[int64]{
						{Value: 1, Attributes: attribute.NewSet(attribute.String("from_channel_name", "primary"), attribute.String("to_channel_name", "fallback"))},
					},
					IsMonotonic: true,
					Temporality: metricdata.CumulativeTemporality,
				},
			},
			{
				Name:        "eef.error_ratio",
				Description: "Ratio of failed calls to total calls for a channel.",
				Unit:        "1",
				Data: metricdata.Gauge[float64]{
					DataPoints: []metricdata.DataPoint[float64]{
						{Value: float64(float32(2) / float32(3)), Attributes: attribute.NewSet(attribute.String("channel_name", "primary"))},
						{Value: 0, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"))},
					},
				},
			},
		}

		// Read metrics after fallback.
		gotMetrics = metricsDataFromReader(context.Background(), t, reader)
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}
	})
}

func TestGCPFallback_ProbeMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader := metric.NewManualReader()
		provider := metric.NewMeterProvider(metric.WithReader(reader))

		primaryResults := []string{"Unavailable", "Unavailable", ""}
		fallbackResults := []string{"Internal", "Unavailable", "", ""}
		primaryIndex := 0
		fallbackIndex := 0

		opts := NewGCPFallbackOptions()
		opts.PrimaryProbingFn = func(cci grpc.ClientConnInterface) string {
			res := primaryResults[primaryIndex]
			primaryIndex++
			return res
		}
		opts.FallbackProbingFn = func(cci grpc.ClientConnInterface) string {
			res := fallbackResults[fallbackIndex]
			fallbackIndex++
			return res
		}
		opts.Period = 5 * time.Second
		opts.PrimaryProbingInterval = 10 * time.Second
		opts.FallbackProbingInterval = 10 * time.Second
		opts.MeterProvider = provider

		_, gcpFallback, primaryConn, _ := setup(t, opts)

		wantMetrics := []metricdata.Metrics{
			{
				Name:        "eef.channel_downtime",
				Description: "How many consecutive seconds probing fails for the channel.",
				Unit:        "s",
				Data: metricdata.Gauge[float64]{
					DataPoints: []metricdata.DataPoint[float64]{
						{Value: 0, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"))},
						{Value: 0, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"))},
					},
				},
			},
		}

		// Read metrics before probing.
		gotMetrics := metricsDataFromReader(context.Background(), t, reader)
		// Verify metrics with values.
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}

		// Wait for the first probing and shift by 4 seconds so that we get some downtime values.
		// This probing should happen only for the fallback channel as we don't probe primary until fallback happens.
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if primaryIndex != 0 {
			t.Fatalf("Primary probe count before fallback must be 0, got: %d", primaryIndex)
		}

		// Read metrics after the first probing.
		gotMetrics = metricsDataFromReader(context.Background(), t, reader)
		// Verify metrics exist.
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars(), metricdatatest.IgnoreValue()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}
		// Verify values.
		for _, dp := range gotMetrics["eef.channel_downtime"].Data.(metricdata.Gauge[float64]).DataPoints {
			cn, _ := dp.Attributes.Value(attribute.Key("channel_name"))
			if cn == attribute.String("channel_name", "primary").Value && dp.Value != 0 {
				t.Errorf("Primary channel downtime after first probe must be 0, got: %f", dp.Value)
			}
			if cn == attribute.String("channel_name", "fallback").Value && (dp.Value < 3.999 || dp.Value > 4.001) {
				t.Errorf("Fallback channel downtime after first probe must be within 3.999-4.001, got: %f", dp.Value)
			}
		}

		// Initiate a fallback.
		expectUnary(t, primaryConn, codes.Unavailable).Times(3)
		for range 3 {
			gcpFallback.Invoke(context.Background(), "method", nil, nil)
		}

		// Wait for the second probing. This time both primary and fallback should be probed.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if primaryIndex != 1 {
			t.Fatalf("Primary probe count after fallback must be 1, got: %d", primaryIndex)
		}
		if fallbackIndex != 2 {
			t.Fatalf("Fallback probe count after fallback must be 2, got: %d", fallbackIndex)
		}

		// Read metrics after the second probing.
		gotMetrics = metricsDataFromReader(context.Background(), t, reader)
		// Verify metrics exist.
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars(), metricdatatest.IgnoreValue()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}
		// Verify values.
		for _, dp := range gotMetrics["eef.channel_downtime"].Data.(metricdata.Gauge[float64]).DataPoints {
			cn, _ := dp.Attributes.Value(attribute.Key("channel_name"))
			if cn == attribute.String("channel_name", "primary").Value && (dp.Value < 3.999 || dp.Value > 4.001) {
				t.Errorf("Primary channel downtime after second probe must be within 3.999-4.001, got: %f", dp.Value)
			}
			if cn == attribute.String("channel_name", "fallback").Value && (dp.Value < 13.999 || dp.Value > 14.001) {
				t.Errorf("Fallback channel downtime after second probe must be within 13.999-14.001, got: %f", dp.Value)
			}
		}

		// Wait for the third probing.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		// Read metrics after the third probing.
		gotMetrics = metricsDataFromReader(context.Background(), t, reader)
		// Verify metrics exist.
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars(), metricdatatest.IgnoreValue()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}
		// Verify values.
		for _, dp := range gotMetrics["eef.channel_downtime"].Data.(metricdata.Gauge[float64]).DataPoints {
			cn, _ := dp.Attributes.Value(attribute.Key("channel_name"))
			if cn == attribute.String("channel_name", "primary").Value && (dp.Value < 13.999 || dp.Value > 14.001) {
				t.Errorf("Primary channel downtime after second probe must be within 13.999-14.001, got: %f", dp.Value)
			}
			if cn == attribute.String("channel_name", "fallback").Value && dp.Value != 0 {
				t.Errorf("Fallback channel downtime after second probe must be 0, got: %f", dp.Value)
			}
		}

		// Wait for the fourth probing.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		// Read metrics after the fourth probing.
		gotMetrics = metricsDataFromReader(context.Background(), t, reader)
		// Verify metrics with values.
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}

		// Verify probe results metric.
		wantMetrics = []metricdata.Metrics{
			{
				Name:        "eef.probe_result",
				Description: "Results of probing functions execution.",
				Unit:        "{result}",
				Data: metricdata.Sum[int64]{
					DataPoints: []metricdata.DataPoint[int64]{
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"), attribute.String("result", ""))},
						{Value: 2, Attributes: attribute.NewSet(attribute.String("channel_name", "primary"), attribute.String("result", "Unavailable"))},
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"), attribute.String("result", "Internal"))},
						{Value: 1, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"), attribute.String("result", "Unavailable"))},
						{Value: 2, Attributes: attribute.NewSet(attribute.String("channel_name", "fallback"), attribute.String("result", ""))},
					},
					IsMonotonic: true,
					Temporality: metricdata.CumulativeTemporality,
				},
			},
		}
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
				t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
			}
		}
	})
}
