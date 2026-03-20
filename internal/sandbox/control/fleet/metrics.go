package fleet

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type fleetMetrics struct {
	// Gauges
	instancesByState  *prometheus.GaugeVec
	sessionsTotal     prometheus.Gauge
	poolSize          prometheus.Gauge
	warmPoolSize      prometheus.Gauge
	healthyInstances  prometheus.Gauge
	pendingProvisions prometheus.Gauge

	// Counters
	provisionsTotal         *prometheus.CounterVec
	terminationsTotal       *prometheus.CounterVec
	createSandboxTotal      *prometheus.CounterVec
	destroySandboxTotal     *prometheus.CounterVec
	healthChecksTotal       *prometheus.CounterVec
	reconciliationsTotal    *prometheus.CounterVec
	drainRecoveriesTotal    prometheus.Counter
	claimSlotRollbacksTotal prometheus.Counter

	// Histograms
	provisionDuration       prometheus.Histogram
	routingDecisionDuration prometheus.Histogram
	createSandboxDuration   prometheus.Histogram
	drainDuration           prometheus.Histogram
	healthCheckDuration     prometheus.Histogram
}

func newFleetMetrics(reg prometheus.Registerer) (*fleetMetrics, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	// --- Gauges ---

	instancesByState, err := registerCollector(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fleet_instances_total",
		Help: "Current number of instances by state.",
	}, []string{"state"}))
	if err != nil {
		return nil, err
	}

	sessionsTotal, err := registerCollector(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "fleet_sessions_total",
		Help: "Total active sessions across all instances.",
	}))
	if err != nil {
		return nil, err
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

	// --- Counters ---

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

	createSandboxTotal, err := registerCollector(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fleet_create_sandbox_total",
		Help: "CreateSandbox calls by result.",
	}, []string{"result"}))
	if err != nil {
		return nil, err
	}

	destroySandboxTotal, err := registerCollector(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fleet_destroy_sandbox_total",
		Help: "DestroySandbox calls by result.",
	}, []string{"result"}))
	if err != nil {
		return nil, err
	}

	healthChecksTotal, err := registerCollector(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fleet_health_checks_total",
		Help: "HealthCheck calls by result.",
	}, []string{"result"}))
	if err != nil {
		return nil, err
	}

	reconciliationsTotal, err := registerCollector(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "fleet_session_reconciliations_total",
		Help: "Session count corrections by direction.",
	}, []string{"direction"}))
	if err != nil {
		return nil, err
	}

	drainRecoveriesTotal, err := registerCollector(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fleet_drain_recoveries_total",
		Help: "Instances recovered from Draining to Ready.",
	}))
	if err != nil {
		return nil, err
	}

	claimSlotRollbacksTotal, err := registerCollector(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fleet_claim_slot_rollbacks_total",
		Help: "Claim-slot rollbacks due to RPC failure.",
	}))
	if err != nil {
		return nil, err
	}

	// --- Histograms ---

	provisionDuration, err := registerCollector(reg, prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleet_provision_duration_seconds",
		Help:    "Provisioning latency in seconds.",
		Buckets: []float64{5, 10, 30, 60, 120, 300, 600},
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

	createSandboxDuration, err := registerCollector(reg, prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleet_create_sandbox_duration_seconds",
		Help:    "End-to-end CreateSandbox latency in seconds.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}))
	if err != nil {
		return nil, err
	}

	drainDuration, err := registerCollector(reg, prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleet_drain_duration_seconds",
		Help:    "Time from Draining to Terminating in seconds.",
		Buckets: []float64{5, 10, 30, 60, 120, 300, 600},
	}))
	if err != nil {
		return nil, err
	}

	healthCheckDuration, err := registerCollector(reg, prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleet_health_check_duration_seconds",
		Help:    "HealthCheck RPC latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}))
	if err != nil {
		return nil, err
	}

	return &fleetMetrics{
		instancesByState:        instancesByState,
		sessionsTotal:           sessionsTotal,
		poolSize:                poolSize,
		warmPoolSize:            warmPoolSize,
		healthyInstances:        healthyInstances,
		pendingProvisions:       pendingProvisions,
		provisionsTotal:         provisionsTotal,
		terminationsTotal:       terminationsTotal,
		createSandboxTotal:      createSandboxTotal,
		destroySandboxTotal:     destroySandboxTotal,
		healthChecksTotal:       healthChecksTotal,
		reconciliationsTotal:    reconciliationsTotal,
		drainRecoveriesTotal:    drainRecoveriesTotal,
		claimSlotRollbacksTotal: claimSlotRollbacksTotal,
		provisionDuration:       provisionDuration,
		routingDecisionDuration: routingDecisionDuration,
		createSandboxDuration:   createSandboxDuration,
		drainDuration:           drainDuration,
		healthCheckDuration:     healthCheckDuration,
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

func (m *fleetMetrics) observeCreateSandboxDuration(d time.Duration) {
	if m == nil {
		return
	}
	m.createSandboxDuration.Observe(d.Seconds())
}

func (m *fleetMetrics) observeDrainDuration(d time.Duration) {
	if m == nil {
		return
	}
	m.drainDuration.Observe(d.Seconds())
}

func (m *fleetMetrics) observeHealthCheckDuration(d time.Duration) {
	if m == nil {
		return
	}
	m.healthCheckDuration.Observe(d.Seconds())
}
