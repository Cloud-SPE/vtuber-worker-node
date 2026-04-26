package backendhttp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
)

func TestMeteredClient_DoJSON_2xxOK(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.JSONStatus = 200
	inner.JSONBody = []byte(`{}`)
	c := WithMetrics(inner, rec, "openai:/v1/chat/completions", "llama-3.3-70b")
	if _, _, err := c.DoJSON(context.Background(), "http://x", nil); err != nil {
		t.Fatal(err)
	}
	if rec.BackendRequests.Load() != 1 || rec.BackendObserves.Load() != 1 {
		t.Fatalf("requests=%d observes=%d", rec.BackendRequests.Load(), rec.BackendObserves.Load())
	}
	if got := rec.LastBackendOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("outcome = %v", got)
	}
	if rec.BackendErrors.Load() != 0 {
		t.Fatalf("expected no errors recorded")
	}
}

func TestMeteredClient_DoJSON_5xxError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.JSONStatus = 502
	c := WithMetrics(inner, rec, "openai:/v1/chat/completions", "llama-3.3-70b")
	if _, _, err := c.DoJSON(context.Background(), "http://x", nil); err != nil {
		t.Fatal(err)
	}
	if rec.BackendRequests.Load() != 1 {
		t.Fatalf("requests = %d", rec.BackendRequests.Load())
	}
	if got := rec.LastBackendOutcome.Load(); got != metrics.OutcomeError {
		t.Fatalf("outcome = %v", got)
	}
	if rec.BackendErrors.Load() != 1 {
		t.Fatalf("expected 1 backend error, got %d", rec.BackendErrors.Load())
	}
	if got := rec.LastBackendErrorClass.Load(); got != metrics.BackendError5xx {
		t.Fatalf("error class = %v", got)
	}
}

func TestMeteredClient_DoJSON_TransportError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.JSONErr = fmt.Errorf("backendhttp: do: %w", errors.New("connection refused"))
	c := WithMetrics(inner, rec, "openai:/v1/chat/completions", "m")
	if _, _, err := c.DoJSON(context.Background(), "http://x", nil); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastBackendOutcome.Load(); got != metrics.OutcomeError {
		t.Fatalf("outcome = %v", got)
	}
	if got := rec.LastBackendErrorClass.Load(); got != metrics.BackendErrorConnect {
		t.Fatalf("error class = %v", got)
	}
}

func TestMeteredClient_DoJSON_TimeoutError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.JSONErr = context.DeadlineExceeded
	c := WithMetrics(inner, rec, "cap", "m")
	if _, _, err := c.DoJSON(context.Background(), "http://x", nil); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastBackendErrorClass.Load(); got != metrics.BackendErrorTimeout {
		t.Fatalf("error class = %v", got)
	}
}

func TestMeteredClient_DoJSON_MalformedError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.JSONErr = errors.New("backendhttp: read body: unexpected EOF")
	c := WithMetrics(inner, rec, "cap", "m")
	if _, _, err := c.DoJSON(context.Background(), "http://x", nil); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastBackendErrorClass.Load(); got != metrics.BackendErrorMalformed {
		t.Fatalf("error class = %v", got)
	}
}

func TestMeteredClient_DoRaw_OK(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.JSONStatus = 200
	c := WithMetrics(inner, rec, "cap", "m")
	if _, _, err := c.DoRaw(context.Background(), "http://x", "multipart/form-data; boundary=abc", nil); err != nil {
		t.Fatal(err)
	}
	if rec.BackendRequests.Load() != 1 {
		t.Fatalf("requests = %d", rec.BackendRequests.Load())
	}
	if got := rec.LastBackendOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_DoStream_OK(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.StreamStatus = 200
	c := WithMetrics(inner, rec, "cap", "m")
	_, _, rd, err := c.DoStream(context.Background(), "http://x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rd != nil {
		_ = rd.Close()
	}
	if rec.BackendRequests.Load() != 1 || rec.BackendObserves.Load() != 1 {
		t.Fatalf("counters off")
	}
	if got := rec.LastBackendOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_DoStream_5xx(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.StreamStatus = 503
	c := WithMetrics(inner, rec, "cap", "m")
	_, _, rd, err := c.DoStream(context.Background(), "http://x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rd != nil {
		_ = rd.Close()
	}
	if got := rec.LastBackendErrorClass.Load(); got != metrics.BackendError5xx {
		t.Fatalf("error class = %v", got)
	}
}

func TestMeteredClient_DoStream_TransportError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.StreamErr = errors.New("dial tcp: connection refused")
	c := WithMetrics(inner, rec, "cap", "m")
	if _, _, _, err := c.DoStream(context.Background(), "http://x", nil); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastBackendErrorClass.Load(); got != metrics.BackendErrorConnect {
		t.Fatalf("error class = %v", got)
	}
}

func TestMeteredClient_NilRecorderReturnsInner(t *testing.T) {
	inner := NewFake()
	if got := WithMetrics(inner, nil, "cap", "m"); got != inner {
		t.Fatal("nil recorder should return the inner client unchanged")
	}
}

func TestClassifyTransportError_NilDefaultsToConnect(t *testing.T) {
	if got := classifyTransportError(nil); got != metrics.BackendErrorConnect {
		t.Fatalf("nil err class = %v", got)
	}
}

func TestClassifyTransportError_TimeoutSubstring(t *testing.T) {
	if got := classifyTransportError(errors.New("net/http: request timeout")); got != metrics.BackendErrorTimeout {
		t.Fatalf("timeout substring class = %v", got)
	}
}
