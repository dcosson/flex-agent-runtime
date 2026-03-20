package fleet

import (
	"context"
	"errors"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newMetricsTestFleet(t *testing.T) (*FleetSandboxControl, *mockProvisioner, *prometheus.Registry) {
	t.Helper()

	reg := prometheus.NewRegistry()
	mp := &mockProvisioner{}
	cfg := DefaultFleetConfig()

	f, err := NewFleetSandboxControl(mp, func(string) (FleetNodeClient, error) {
		return &mockNodeClient{}, nil
	}, cfg, WithMetricsRegisterer(reg))
	if err != nil {
		t.Fatalf("NewFleetSandboxControl() error: %v", err)
	}
	return f, mp, reg
}

func TestFleetMetrics_GaugesReflectSnapshot(t *testing.T) {
	f, _, _ := newMetricsTestFleet(t)

	addInstance(f, "i-ready", "10.0.1.1", InstanceReady, 0, &mockNodeClient{})
	addInstance(f, "i-active", "10.0.1.2", InstanceActive, 2, &mockNodeClient{})
	addInstance(f, "i-drain", "10.0.1.3", InstanceDraining, 1, &mockNodeClient{})
	f.pendingLaunches.Store(2)

	f.updateMetricsSnapshot()

	if got := testutil.ToFloat64(f.metrics.poolSize); got != 3 {
		t.Fatalf("fleet_pool_size = %v, want 3", got)
	}
	if got := testutil.ToFloat64(f.metrics.warmPoolSize); got != 1 {
		t.Fatalf("fleet_warm_pool_size = %v, want 1", got)
	}
	if got := testutil.ToFloat64(f.metrics.healthyInstances); got != 2 {
		t.Fatalf("fleet_healthy_instances = %v, want 2", got)
	}
	if got := testutil.ToFloat64(f.metrics.pendingProvisions); got != 2 {
		t.Fatalf("fleet_pending_provisions = %v, want 2", got)
	}
}

func TestFleetMetrics_ProvisionCounterAndHistogramsEmit(t *testing.T) {
	f, _, reg := newMetricsTestFleet(t)

	if err := f.provisionInstance(context.Background()); err != nil {
		t.Fatalf("provisionInstance() error: %v", err)
	}

	if got := testutil.ToFloat64(f.metrics.provisionsTotal.WithLabelValues("success")); got != 1 {
		t.Fatalf("fleet_provisions_total{result=success} = %v, want 1", got)
	}

	assertHistogramHasSamples(t, reg, "fleet_provision_duration_seconds", 1)
}

func TestFleetMetrics_ProvisionDurationRecordedOnError(t *testing.T) {
	f, mp, reg := newMetricsTestFleet(t)
	mp.launchFn = func(context.Context, instance.InstanceConfig) (*instance.InstanceInfo, error) {
		return nil, errors.New("launch failed")
	}

	if err := f.provisionInstance(context.Background()); err == nil {
		t.Fatal("expected provisionInstance() error")
	}

	if got := testutil.ToFloat64(f.metrics.provisionsTotal.WithLabelValues("error")); got != 1 {
		t.Fatalf("fleet_provisions_total{result=error} = %v, want 1", got)
	}
	assertHistogramHasSamples(t, reg, "fleet_provision_duration_seconds", 1)
}

func TestFleetMetrics_RoutingAndTerminationMetricsEmit(t *testing.T) {
	f, _, reg := newMetricsTestFleet(t)

	addInstance(f, "i-ready", "10.0.1.1", InstanceReady, 0, &mockNodeClient{})
	if _, err := f.CreateSandbox(context.Background(), control.CreateSandboxRequest{}); err != nil {
		t.Fatalf("CreateSandbox() error: %v", err)
	}
	assertHistogramHasSamples(t, reg, "fleet_routing_decision_duration_seconds", 1)

	addInstance(f, "i-ready-2", "10.0.1.2", InstanceReady, 0, &mockNodeClient{})
	if err := f.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if got := testutil.ToFloat64(f.metrics.terminationsTotal.WithLabelValues("shutdown")); got != 2 {
		t.Fatalf("fleet_terminations_total{reason=shutdown} = %v, want 2", got)
	}
}

func assertHistogramHasSamples(t *testing.T, reg *prometheus.Registry, metricName string, min uint64) {
	t.Helper()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != metricName {
			continue
		}
		if len(fam.Metric) == 0 || fam.Metric[0].Histogram == nil {
			t.Fatalf("%s is missing histogram datapoints", metricName)
		}
		if got := fam.Metric[0].Histogram.GetSampleCount(); got < min {
			t.Fatalf("%s sample_count = %d, want >= %d", metricName, got, min)
		}
		return
	}

	t.Fatalf("metric %s not found", metricName)
}

func TestFleetMetrics_ProvisionHistogramBuckets(t *testing.T) {
	_, _, reg := newMetricsTestFleet(t)
	want := []float64{5, 10, 30, 60, 120, 300, 600}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != "fleet_provision_duration_seconds" {
			continue
		}
		if len(fam.Metric) == 0 || fam.Metric[0].Histogram == nil {
			t.Fatal("fleet_provision_duration_seconds missing histogram data")
		}
		buckets := fam.Metric[0].Histogram.Bucket
		if len(buckets) != len(want) {
			t.Fatalf("bucket count = %d, want %d", len(buckets), len(want))
		}
		for i, b := range buckets {
			if b.GetUpperBound() != want[i] {
				t.Fatalf("bucket[%d] upper bound = %v, want %v", i, b.GetUpperBound(), want[i])
			}
		}
		return
	}
	t.Fatal("fleet_provision_duration_seconds not found")
}
