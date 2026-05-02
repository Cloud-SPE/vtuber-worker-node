package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPrometheus_HandlerExposesNamespace(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{MaxSeriesPerMetric: 100})
	r.IncRequest("chat", "gpt-4o-mini", OutcomeRequest2xx)
	r.AddWorkUnits("chat", "gpt-4o-mini", UnitToken, 42)
	r.IncDaemonRPC(MethodProcessPayment, OutcomeOK)
	r.IncBackendRequest("chat", "gpt-4o-mini", OutcomeOK)
	r.IncTokenizerCall("gpt-4o-mini", OutcomeOK)
	r.IncCapacityRejection("chat")
	r.IncPaymentRejection(PaymentRejectInsufficientBalance)
	r.SetBuildInfo("v0.0.0", "v1", "go1.25")

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body := readAll(t, resp)
	mustContain(t, body, "livepeer_worker_requests_total")
	mustContain(t, body, `capability="chat"`)
	mustContain(t, body, `model="gpt-4o-mini"`)
	mustContain(t, body, "livepeer_worker_work_units_total")
	mustContain(t, body, `unit="token"`)
	mustContain(t, body, "livepeer_worker_daemon_rpc_calls_total")
	mustContain(t, body, "livepeer_worker_backend_requests_total")
	mustContain(t, body, "livepeer_worker_tokenizer_calls_total")
	mustContain(t, body, "livepeer_worker_capacity_rejections_total")
	mustContain(t, body, "livepeer_worker_payment_rejections_total")
	mustContain(t, body, "livepeer_worker_build_info")
	// Standard collectors
	mustContain(t, body, "go_goroutines")
	mustContain(t, body, "process_cpu_seconds_total")
}

func TestPrometheus_RegistryIsolated(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	if r.Registry() == nil {
		t.Fatal("Registry() returned nil")
	}
	if r.Registry() == prometheus.DefaultRegisterer {
		t.Fatal("Registry() returned the default registry; must be private")
	}

	// Confirm a metric registered on the default registerer is not
	// surfaced by our handler — proves true isolation.
	defaultOnly := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "should_not_appear_on_worker_metrics",
		Help: "test",
	})
	if err := prometheus.Register(defaultOnly); err != nil {
		t.Fatal(err)
	}
	defer prometheus.Unregister(defaultOnly)
	defaultOnly.Inc()

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	if strings.Contains(body, "should_not_appear_on_worker_metrics") {
		t.Fatalf("private registry leaked default-registerer metric")
	}
}

func TestPrometheus_EmptyLabelBecomesUnset(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.IncRequest("chat", "gpt-4o-mini", "") // empty outcome

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	mustContain(t, body, `outcome="`+LabelUnset+`"`)
}

func TestPrometheus_CardinalityCapEnforced(t *testing.T) {
	var capHits []string
	r := NewPrometheus(PrometheusConfig{
		MaxSeriesPerMetric: 2,
		OnCapExceeded: func(metricName string, observed, cap int) {
			capHits = append(capHits, metricName)
		},
	})
	// Two distinct values: pass.
	r.IncCapacityRejection("a")
	r.IncCapacityRejection("b")
	// Third distinct value: capped.
	r.IncCapacityRejection("c")
	// Re-using an existing label still works.
	r.IncCapacityRejection("a")
	r.IncCapacityRejection("a")
	// Fourth and fifth distinct values: still capped, but only one
	// callback fires per metric (deduped).
	r.IncCapacityRejection("d")
	r.IncCapacityRejection("e")

	if len(capHits) != 1 || !strings.Contains(capHits[0], "capacity_rejections_total") {
		t.Fatalf("expected exactly one cap-hit notification for capacity_rejections_total, got %v", capHits)
	}

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	mustContain(t, body, `capability="a"`)
	mustContain(t, body, `capability="b"`)
	if strings.Contains(body, `capability="c"`) {
		t.Fatalf("expected capability=c to be dropped:\n%s", body)
	}
	if strings.Contains(body, `capability="d"`) {
		t.Fatalf("expected capability=d to be dropped")
	}
}

func TestPrometheus_CardinalityCapAppliesToAdd(t *testing.T) {
	// AddWorkUnits goes through the cap wrapper's add() path;
	// confirm it enforces cardinality the same way inc() does.
	r := NewPrometheus(PrometheusConfig{MaxSeriesPerMetric: 1})
	r.AddWorkUnits("chat", "model-a", UnitToken, 10)
	r.AddWorkUnits("chat", "model-b", UnitToken, 20) // dropped
	r.AddWorkUnits("chat", "model-a", UnitToken, 5)  // accepted (existing tuple)

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	mustContain(t, body, `livepeer_worker_work_units_total{capability="chat",model="model-a",unit="token"} 15`)
	if strings.Contains(body, `model="model-b"`) {
		t.Fatalf("expected model-b tuple to be dropped past the cap")
	}
}

func TestPrometheus_CapDisabled(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{MaxSeriesPerMetric: 0})
	for i := 0; i < 50; i++ {
		r.IncCapacityRejection(string(rune('a' + i%26)))
	}
	// No assertion — just confirms zero cap doesn't panic and labels
	// are accepted freely.
}

func TestPrometheus_DaemonRPCDualHistogram(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.ObserveDaemonRPC(MethodProcessPayment, OutcomeOK, 250*time.Microsecond)

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	// Both histograms must record the observation. Confirm count=1
	// in each rather than just bucket presence so we know the call
	// actually dual-fired.
	mustContain(t, body, `livepeer_worker_daemon_rpc_duration_seconds_count{method="ProcessPayment",outcome="ok"} 1`)
	mustContain(t, body, `livepeer_worker_daemon_rpc_duration_seconds_fast_count{method="ProcessPayment",outcome="ok"} 1`)
}

func TestPrometheus_HistogramsRecord(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.ObserveRequest("chat", "gpt-4o-mini", OutcomeRequest2xx, 10*time.Millisecond)
	r.ObserveBackendRequest("chat", "gpt-4o-mini", 50*time.Millisecond)
	r.ObserveDaemonRPC(MethodDebitBalance, OutcomeOK, 500*time.Microsecond)

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	mustContain(t, body, "livepeer_worker_request_duration_seconds_bucket")
	mustContain(t, body, "livepeer_worker_backend_request_duration_seconds_bucket")
	mustContain(t, body, "livepeer_worker_daemon_rpc_duration_seconds_bucket")
	mustContain(t, body, "livepeer_worker_daemon_rpc_duration_seconds_fast_bucket")
}

func TestPrometheus_GaugesSet(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.SetInflightRequests(7)
	r.SetMaxConcurrent(64)
	r.SetUptimeSeconds(123.4)
	r.SetBackendLastSuccess("chat", "gpt-4o-mini", time.Unix(1745000000, 0))

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	mustContain(t, body, "livepeer_worker_inflight_requests 7")
	mustContain(t, body, "livepeer_worker_max_concurrent 64")
	mustContain(t, body, "livepeer_worker_uptime_seconds 123.4")
	mustContain(t, body, `livepeer_worker_backend_last_success_timestamp_seconds{capability="chat",model="gpt-4o-mini"} 1.745e+09`)
}

func TestPrometheus_AddWorkUnits_NonPositiveSkipped(t *testing.T) {
	// Calls with n<=0 should not register a series.
	r := NewPrometheus(PrometheusConfig{})
	r.AddWorkUnits("chat", "m", UnitToken, 0)
	r.AddWorkUnits("chat", "m", UnitToken, -5)

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	if strings.Contains(body, "livepeer_worker_work_units_total{") {
		t.Fatalf("non-positive AddWorkUnits should not create a series")
	}
}

func TestPrometheus_BackendErrorAccounting(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.IncBackendRequest("chat", "m", OutcomeError)
	r.IncBackendError("chat", "m", BackendErrorTimeout)

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)
	mustContain(t, body, `livepeer_worker_backend_requests_total{capability="chat",model="m",outcome="error"} 1`)
	mustContain(t, body, `livepeer_worker_backend_errors_total{capability="chat",error_class="timeout",model="m"} 1`)
}

func TestUnsetEmptyString(t *testing.T) {
	if got := unset(""); got != LabelUnset {
		t.Fatalf("got %q", got)
	}
	if got := unset("ok"); got != "ok" {
		t.Fatalf("got %q", got)
	}
}

func TestJoinNul(t *testing.T) {
	a := joinNul([]string{"a", "b"})
	b := joinNul([]string{"ab", ""})
	if a == b {
		t.Fatalf("nul-separator collision: a=%q b=%q", a, b)
	}
}

// --- helpers ---

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustContain(t *testing.T, body, sub string) {
	t.Helper()
	if !strings.Contains(body, sub) {
		t.Fatalf("body missing %q. body:\n%s", sub, body)
	}
}
