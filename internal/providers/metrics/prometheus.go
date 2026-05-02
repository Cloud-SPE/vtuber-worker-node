package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusConfig captures the construction parameters.
type PrometheusConfig struct {
	// MaxSeriesPerMetric is the hard cap on distinct label tuples
	// any single MetricVec may track. New combinations beyond the
	// cap are silently dropped (their values do not propagate to
	// Prometheus); existing combinations continue to update. Set to
	// 0 to disable the cap.
	MaxSeriesPerMetric int

	// OnCapExceeded, if non-nil, is invoked once per exceeded metric
	// (deduped). Operators wire this to their structured logger so
	// the violation is loud in the daemon log.
	OnCapExceeded func(metricName string, observed int, cap int)
}

// Prometheus is the production Recorder. All metrics live in a
// single, dedicated *prometheus.Registry that we own — we do NOT use
// the package-global default registry, so noisy consumer libs don't
// pollute our exposition output.
type Prometheus struct {
	reg *prometheus.Registry
	cfg PrometheusConfig

	// Counters
	requests           *capVec
	workUnits          *capVec
	daemonRPCCalls     *capVec
	backendRequests    *capVec
	backendErrors      *capVec
	tokenizerCalls     *capVec
	capacityRejections *capVec
	paymentRejections  *capVec

	// Histograms
	requestDuration       *prometheus.HistogramVec
	daemonRPCDuration     *prometheus.HistogramVec
	daemonRPCDurationFast *prometheus.HistogramVec
	backendDuration       *prometheus.HistogramVec

	// Gauges
	inflightRequests   prometheus.Gauge
	backendLastSuccess *prometheus.GaugeVec
	uptimeSeconds      prometheus.Gauge
	buildInfo          *prometheus.GaugeVec
	maxConcurrent      prometheus.Gauge
}

// NewPrometheus constructs the Prometheus Recorder. It also installs
// the standard process + Go runtime collectors so /metrics surfaces
// `go_*` and `process_*` for free.
func NewPrometheus(cfg PrometheusConfig) *Prometheus {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	p := &Prometheus{reg: reg, cfg: cfg}
	const ns = "livepeer_worker"

	// ----- Counters -----
	p.requests = newCap(reg, p.onCapHit, "requests_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "requests_total",
			Help: "Total customer-facing requests served, labeled by capability/model/outcome."},
		[]string{"capability", "model", "outcome"},
	))
	p.workUnits = newCap(reg, p.onCapHit, "work_units_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "work_units_total",
			Help: "Total work units billed (the revenue signal). Joined against bridge revenue for margin."},
		[]string{"capability", "model", "unit"},
	))
	p.daemonRPCCalls = newCap(reg, p.onCapHit, "daemon_rpc_calls_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "daemon_rpc_calls_total",
			Help: "Total payee-daemon unix-socket gRPC calls, labeled by method + outcome."},
		[]string{"method", "outcome"},
	))
	p.backendRequests = newCap(reg, p.onCapHit, "backend_requests_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "backend_requests_total",
			Help: "Total upstream model-backend HTTP calls, labeled by capability/model/outcome."},
		[]string{"capability", "model", "outcome"},
	))
	p.backendErrors = newCap(reg, p.onCapHit, "backend_errors_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "backend_errors_total",
			Help: "Backend errors classified by error_class={timeout,5xx,malformed,connect}."},
		[]string{"capability", "model", "error_class"},
	))
	p.tokenizerCalls = newCap(reg, p.onCapHit, "tokenizer_calls_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "tokenizer_calls_total",
			Help: "Total tokenizer invocations, labeled by model + outcome."},
		[]string{"model", "outcome"},
	))
	p.capacityRejections = newCap(reg, p.onCapHit, "capacity_rejections_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "capacity_rejections_total",
			Help: "503s emitted because the inflight semaphore was full."},
		[]string{"capability"},
	))
	p.paymentRejections = newCap(reg, p.onCapHit, "payment_rejections_total", prometheus.NewCounterVec(
		prometheus.CounterOpts{Namespace: ns, Name: "payment_rejections_total",
			Help: "402s emitted by paymentMiddleware, labeled by reason."},
		[]string{"reason"},
	))

	// ----- Histograms -----
	p.requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "request_duration_seconds",
			Help:    "End-to-end request latency (default Prometheus buckets).",
			Buckets: prometheus.DefBuckets},
		[]string{"capability", "model", "outcome"},
	)
	reg.MustRegister(p.requestDuration)

	p.daemonRPCDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "daemon_rpc_duration_seconds",
			Help:    "Payee-daemon RPC latency (default Prometheus buckets).",
			Buckets: prometheus.DefBuckets},
		[]string{"method", "outcome"},
	)
	reg.MustRegister(p.daemonRPCDuration)

	// Sub-millisecond variant for the unix-socket fast path
	// (cached responses return in ~50µs–500µs).
	p.daemonRPCDurationFast = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "daemon_rpc_duration_seconds_fast",
			Help:    "Payee-daemon RPC latency, sub-ms buckets for the unix-socket fast path.",
			Buckets: []float64{0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01}},
		[]string{"method", "outcome"},
	)
	reg.MustRegister(p.daemonRPCDurationFast)

	p.backendDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: ns, Name: "backend_request_duration_seconds",
			Help:    "Upstream model-backend HTTP round-trip latency.",
			Buckets: prometheus.DefBuckets},
		[]string{"capability", "model"},
	)
	reg.MustRegister(p.backendDuration)

	// ----- Gauges -----
	p.inflightRequests = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "inflight_requests",
		Help: "Current number of in-flight customer-facing requests (semaphore depth).",
	})
	reg.MustRegister(p.inflightRequests)

	p.backendLastSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Namespace: ns, Name: "backend_last_success_timestamp_seconds",
			Help: "Unix timestamp of the most recent successful backend response by (capability, model)."},
		[]string{"capability", "model"},
	)
	reg.MustRegister(p.backendLastSuccess)

	p.uptimeSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "uptime_seconds",
		Help: "Seconds since worker start.",
	})
	reg.MustRegister(p.uptimeSeconds)

	p.buildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Namespace: ns, Name: "build_info",
			Help: "Constant 1 gauge labeled with worker build metadata."},
		[]string{"version", "protocol_version", "go_version"},
	)
	reg.MustRegister(p.buildInfo)

	p.maxConcurrent = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "max_concurrent",
		Help: "Configured maximum concurrent requests (semaphore size).",
	})
	reg.MustRegister(p.maxConcurrent)

	p.ApplyCap(cfg.MaxSeriesPerMetric)
	return p
}

// onCapHit dispatches to the user-supplied callback. Wrapped because
// it's accessed from many newCap closures.
func (p *Prometheus) onCapHit(name string, observed, cap int) {
	if p.cfg.OnCapExceeded != nil {
		p.cfg.OnCapExceeded(name, observed, cap)
	}
}

// Registry returns the underlying *prometheus.Registry. Exposed so
// runtime/metrics can serve it via promhttp.HandlerFor.
func (p *Prometheus) Registry() *prometheus.Registry { return p.reg }

// Handler returns a promhttp handler over our private registry.
func (p *Prometheus) Handler() http.Handler {
	return promhttp.HandlerFor(p.reg, promhttp.HandlerOpts{})
}

// ----- Recorder method implementations -----

func (p *Prometheus) IncRequest(capability, model, outcome string) {
	p.requests.inc(unset(capability), unset(model), unset(outcome))
}

func (p *Prometheus) ObserveRequest(capability, model, outcome string, d time.Duration) {
	p.requestDuration.WithLabelValues(unset(capability), unset(model), unset(outcome)).Observe(d.Seconds())
}

func (p *Prometheus) AddWorkUnits(capability, model, unit string, n int64) {
	if n <= 0 {
		return
	}
	// Direct CounterVec.Add path; the cap wrapper only exposes Inc,
	// so we replicate the seen-tuple bookkeeping here.
	p.workUnits.add(float64(n), unset(capability), unset(model), unset(unit))
}

func (p *Prometheus) IncDaemonRPC(method, outcome string) {
	p.daemonRPCCalls.inc(unset(method), unset(outcome))
}

func (p *Prometheus) ObserveDaemonRPC(method, outcome string, d time.Duration) {
	m := unset(method)
	o := unset(outcome)
	p.daemonRPCDuration.WithLabelValues(m, o).Observe(d.Seconds())
	p.daemonRPCDurationFast.WithLabelValues(m, o).Observe(d.Seconds())
}

func (p *Prometheus) IncBackendRequest(capability, model, outcome string) {
	p.backendRequests.inc(unset(capability), unset(model), unset(outcome))
}

func (p *Prometheus) ObserveBackendRequest(capability, model string, d time.Duration) {
	p.backendDuration.WithLabelValues(unset(capability), unset(model)).Observe(d.Seconds())
}

func (p *Prometheus) IncBackendError(capability, model, errorClass string) {
	p.backendErrors.inc(unset(capability), unset(model), unset(errorClass))
}

func (p *Prometheus) SetBackendLastSuccess(capability, model string, t time.Time) {
	p.backendLastSuccess.WithLabelValues(unset(capability), unset(model)).Set(float64(t.Unix()))
}

func (p *Prometheus) IncTokenizerCall(model, outcome string) {
	p.tokenizerCalls.inc(unset(model), unset(outcome))
}

func (p *Prometheus) IncCapacityRejection(capability string) {
	p.capacityRejections.inc(unset(capability))
}

func (p *Prometheus) SetInflightRequests(n int) {
	p.inflightRequests.Set(float64(n))
}

func (p *Prometheus) IncPaymentRejection(reason string) {
	p.paymentRejections.inc(unset(reason))
}

func (p *Prometheus) SetUptimeSeconds(s float64) { p.uptimeSeconds.Set(s) }

func (p *Prometheus) SetBuildInfo(version, protocolVersion, goVersion string) {
	p.buildInfo.WithLabelValues(version, protocolVersion, goVersion).Set(1)
}

func (p *Prometheus) SetMaxConcurrent(n int) { p.maxConcurrent.Set(float64(n)) }

// unset returns LabelUnset if v is empty. Prometheus accepts empty
// strings as label values but they read poorly in Grafana.
func unset(v string) string {
	if v == "" {
		return LabelUnset
	}
	return v
}

// ----- cardinality cap -----

// capVec wraps a CounterVec with cardinality enforcement. Tracks
// distinct label tuples in a sync.Map; if the count exceeds the cap,
// new label combinations are silently dropped (existing combinations
// still update).
type capVec struct {
	vec      *prometheus.CounterVec
	name     string
	max      int
	seen     sync.Map // map[string]struct{} — key = "v1\x00v2\x00..."
	count    atomic.Int64
	exceeded atomic.Bool
	onExceed func(name string, observed, cap int)
}

func newCap(reg prometheus.Registerer, onExceed func(string, int, int), name string, v *prometheus.CounterVec) *capVec {
	reg.MustRegister(v)
	return &capVec{vec: v, name: name, onExceed: onExceed}
}

// withCap can be set by NewPrometheus after construction; the
// capVecs are built before cfg is fully wired so we set max here.
func (c *capVec) withCap(max int) *capVec { c.max = max; return c }

// inc increments the counter at (vals...) labels, enforcing the cap.
// If the cap is 0 (disabled) or the label tuple has been seen before,
// fast path. Otherwise check the count.
func (c *capVec) inc(vals ...string) {
	if c.allow(vals) {
		c.vec.WithLabelValues(vals...).Inc()
	}
}

// add increments by an arbitrary delta — used for AddWorkUnits.
func (c *capVec) add(delta float64, vals ...string) {
	if c.allow(vals) {
		c.vec.WithLabelValues(vals...).Add(delta)
	}
}

// allow returns true if the label tuple is allowed under the cap.
// Centralizes the cap bookkeeping so inc and add stay symmetric.
func (c *capVec) allow(vals []string) bool {
	if c.max <= 0 {
		return true
	}
	key := joinNul(vals)
	if _, ok := c.seen.Load(key); ok {
		return true
	}
	if c.count.Load() >= int64(c.max) {
		// Cap reached. First-time-only log.
		if c.exceeded.CompareAndSwap(false, true) && c.onExceed != nil {
			c.onExceed(c.name, int(c.count.Load()), c.max)
		}
		return false
	}
	c.seen.Store(key, struct{}{})
	c.count.Add(1)
	return true
}

// joinNul concatenates label values with NUL separators. NUL is not
// permitted in Prometheus label values so this is collision-free.
func joinNul(vs []string) string {
	n := 0
	for _, v := range vs {
		n += len(v) + 1
	}
	out := make([]byte, 0, n)
	for _, v := range vs {
		out = append(out, v...)
		out = append(out, 0)
	}
	return string(out)
}

// ApplyCap sets the max-series-per-metric cap on every wrapped vec.
// Called by NewPrometheus after all vecs are built.
func (p *Prometheus) ApplyCap(max int) {
	for _, v := range []*capVec{
		p.requests, p.workUnits, p.daemonRPCCalls,
		p.backendRequests, p.backendErrors, p.tokenizerCalls,
		p.capacityRejections, p.paymentRejections,
	} {
		v.withCap(max)
	}
}
