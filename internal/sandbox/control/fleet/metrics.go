package fleet

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type fleetMetrics struct {
	poolSize          prometheus.Gauge
	warmPoolSize      prometheus.Gauge
	healthyInstances  prometheus.Gauge
	pendingProvisions prometheus.Gauge

	provisionsTotal   *prometheus.CounterVec
	terminationsTotal *prometheus.CounterVec

	provisionDuration       prometheus.Histogram
	routingDecisionDuration prometheus.Histogram
}

func newFleetMetrics(reg prometheus.Registerer) (*fleetMetrics, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	poolSize, err := registerCollector(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fleet_pool_size",
		Help: "Current number of managed fleet instances.",
	}))
	if err != nil {
		return nil, err
	}

	warmPoolSize, err := registerCollector(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fleet_warm_pool_size",
		Help: "Current number of idle ready instances in the warm pool.",
	}))
	if err != nil {
		return nil, err
	}

	healthyInstances, err := registerCollector(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fleet_healthy_instances",
		Help: "Current number of healthy routable instances (ready or active).",
	}))
	if err != nil {
		return nil, err
	}

	pendingProvisions, err := registerCollector(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fleet_pending_provisions",
		Help: "Current number of in-flight LaunchInstance operations.",
	}))
	if err != nil {
		return nil, err
	}

	provisionsTotal, err := registerCollector(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fleet_provisions_total",
		Help: "Total number of LaunchInstance attempts by result.",
	}, []string{"result"}))
	if err != nil {
		return nil, err
	}

	terminationsTotal, err := registerCollector(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fleet_terminations_total",
		Help: "Total number of TerminateInstance calls by reason.",
	}, []string{"reason"}))
	if err != nil {
		return nil, err
	}

	provisionDuration, err := registerCollector(reg, prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleet_provision_duration_seconds",
		Help:    "Provisioning latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}))
	if err != nil {
		return nil, err
	}

	routingDecisionDuration, err := registerCollector(reg, prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleet_routing_decision_duration_seconds",
		Help:    "Routing decision latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}))
	if err != nil {
		return nil, err
	}

	return &fleetMetrics{
		poolSize:                poolSize,
		warmPoolSize:            warmPoolSize,
		healthyInstances:        healthyInstances,
		pendingProvisions:       pendingProvisions,
		provisionsTotal:         provisionsTotal,
		terminationsTotal:       terminationsTotal,
		provisionDuration:       provisionDuration,
		routingDecisionDuration: routingDecisionDuration,
	}, nil
}

func registerCollector[T prometheus.Collector](reg prometheus.Registerer, collector T) (T, error) {
	if err := reg.Register(collector); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			existing, ok := are.ExistingCollector.(T)
			if !ok {
				var zero T
				return zero, err
			}
			return existing, nil
		}
		var zero T
		return zero, err
	}
	return collector, nil
}

func (m *fleetMetrics) observeRoutingDecision(d time.Duration) {
	if m == nil {
		return
	}
	m.routingDecisionDuration.Observe(d.Seconds())
}

func (m *fleetMetrics) observeProvisionDuration(d time.Duration) {
	if m == nil {
		return
	}
	m.provisionDuration.Observe(d.Seconds())
}
