package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNoop_AllMethodsAreSafe(t *testing.T) {
	r := NewNoop()
	r.IncRequest("chat", "m", OutcomeRequest2xx)
	r.ObserveRequest("chat", "m", OutcomeRequest2xx, time.Millisecond)
	r.AddWorkUnits("chat", "m", UnitToken, 5)
	r.IncDaemonRPC(MethodProcessPayment, OutcomeOK)
	r.ObserveDaemonRPC(MethodProcessPayment, OutcomeOK, time.Microsecond)
	r.IncBackendRequest("chat", "m", OutcomeOK)
	r.ObserveBackendRequest("chat", "m", time.Millisecond)
	r.IncBackendError("chat", "m", BackendErrorTimeout)
	r.SetBackendLastSuccess("chat", "m", time.Now())
	r.IncTokenizerCall("m", OutcomeOK)
	r.IncCapacityRejection("chat")
	r.SetInflightRequests(3)
	r.IncPaymentRejection(PaymentRejectInsufficientBalance)
	r.SetUptimeSeconds(10)
	r.SetBuildInfo("v0.0.0", "v1", "go1.25")
	r.SetMaxConcurrent(64)
}

func TestNoop_HandlerReturns404(t *testing.T) {
	srv := httptest.NewServer(NewNoop().Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// TestCounter exercises every Counter method so the test helper
// itself doesn't bring the metrics package below the coverage floor.
func TestCounter(t *testing.T) {
	c := NewCounter()

	c.IncRequest("chat", "m", OutcomeRequest2xx)
	c.ObserveRequest("chat", "m", OutcomeRequest2xx, time.Millisecond)
	c.AddWorkUnits("chat", "m", UnitToken, 42)
	c.IncDaemonRPC(MethodProcessPayment, OutcomeOK)
	c.ObserveDaemonRPC(MethodProcessPayment, OutcomeOK, time.Microsecond)
	c.IncBackendRequest("chat", "m", OutcomeOK)
	c.ObserveBackendRequest("chat", "m", time.Millisecond)
	c.IncBackendError("chat", "m", BackendErrorTimeout)
	c.SetBackendLastSuccess("chat", "m", time.Now())
	c.IncTokenizerCall("m", OutcomeOK)
	c.IncCapacityRejection("chat")
	c.SetInflightRequests(3)
	c.IncPaymentRejection(PaymentRejectInsufficientBalance)
	c.SetUptimeSeconds(10)
	c.SetBuildInfo("v0.0.0", "v1", "go1.25")
	c.SetMaxConcurrent(64)

	if c.Requests.Load() != 1 || c.RequestObserves.Load() != 1 {
		t.Fatalf("request counters off: %d / %d", c.Requests.Load(), c.RequestObserves.Load())
	}
	if c.WorkUnitsAdds.Load() != 1 || c.WorkUnitsTotal.Load() != 42 {
		t.Fatalf("work-units counters off: adds=%d total=%d", c.WorkUnitsAdds.Load(), c.WorkUnitsTotal.Load())
	}
	if c.DaemonRPCCalls.Load() != 1 || c.DaemonRPCObserves.Load() != 1 {
		t.Fatalf("daemon-rpc counters off")
	}
	if c.BackendRequests.Load() != 1 || c.BackendErrors.Load() != 1 || c.BackendObserves.Load() != 1 {
		t.Fatalf("backend counters off")
	}
	if c.TokenizerCalls.Load() != 1 || c.CapacityRejections.Load() != 1 || c.PaymentRejections.Load() != 1 {
		t.Fatalf("misc counters off")
	}
	if c.InflightV.Load() != 3 || c.MaxConcurrentV.Load() != 64 {
		t.Fatalf("gauges off")
	}
	if got := c.LastRequestOutcome.Load(); got != OutcomeRequest2xx {
		t.Fatalf("last request outcome = %v", got)
	}
	if got := c.LastDaemonRPCMethod.Load(); got != MethodProcessPayment {
		t.Fatalf("last daemon method = %v", got)
	}
	if got := c.LastBackendErrorClass.Load(); got != BackendErrorTimeout {
		t.Fatalf("last backend error class = %v", got)
	}
	if got := c.LastPaymentReason.Load(); got != PaymentRejectInsufficientBalance {
		t.Fatalf("last payment reason = %v", got)
	}
	if got := c.LastWorkUnitsUnit.Load(); got != UnitToken {
		t.Fatalf("last work-units unit = %v", got)
	}

	if h := c.Handler(); h == nil {
		t.Fatal("nil handler")
	}
}
