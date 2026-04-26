package payeedaemon

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
)

func TestMeteredClient_ListCapabilitiesOK(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	c := WithMetrics(inner, rec)
	if _, err := c.ListCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.DaemonRPCCalls.Load() != 1 || rec.DaemonRPCObserves.Load() != 1 {
		t.Fatalf("calls=%d observes=%d", rec.DaemonRPCCalls.Load(), rec.DaemonRPCObserves.Load())
	}
	if got := rec.LastDaemonRPCMethod.Load(); got != metrics.MethodListCapabilities {
		t.Fatalf("method = %v", got)
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_ListCapabilitiesError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.ListCapabilitiesError = errors.New("boom")
	c := WithMetrics(inner, rec)
	if _, err := c.ListCapabilities(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeError {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_GetQuoteOK(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	c := WithMetrics(inner, rec)
	if _, err := c.GetQuote(context.Background(), []byte{0x01}, "openai:/v1/chat/completions"); err != nil {
		t.Fatal(err)
	}
	if got := rec.LastDaemonRPCMethod.Load(); got != metrics.MethodGetQuote {
		t.Fatalf("method = %v", got)
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_GetQuoteError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.GetQuoteError = errors.New("boom")
	c := WithMetrics(inner, rec)
	if _, err := c.GetQuote(context.Background(), nil, "x"); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeError {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_ProcessPaymentOK(t *testing.T) {
	rec := metrics.NewCounter()
	c := WithMetrics(NewFake(), rec)
	if _, err := c.ProcessPayment(context.Background(), []byte{1}, "wid"); err != nil {
		t.Fatal(err)
	}
	if got := rec.LastDaemonRPCMethod.Load(); got != metrics.MethodProcessPayment {
		t.Fatalf("method = %v", got)
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_ProcessPaymentError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.ProcessPaymentError = errors.New("boom")
	c := WithMetrics(inner, rec)
	if _, err := c.ProcessPayment(context.Background(), nil, ""); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeError {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_DebitBalanceOK(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	// Pre-credit so DebitBalance has a balance to debit; not strictly
	// needed (the fake doesn't gate on balance), but mirrors realistic
	// usage.
	if _, err := inner.ProcessPayment(context.Background(), []byte{1}, "wid"); err != nil {
		t.Fatal(err)
	}
	c := WithMetrics(inner, rec)
	if _, err := c.DebitBalance(context.Background(), inner.SenderAddress, "wid", 1); err != nil {
		t.Fatal(err)
	}
	if got := rec.LastDaemonRPCMethod.Load(); got != metrics.MethodDebitBalance {
		t.Fatalf("method = %v", got)
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_DebitBalanceError(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	inner.DebitBalanceError = errors.New("boom")
	c := WithMetrics(inner, rec)
	if _, err := c.DebitBalance(context.Background(), nil, "", 1); err == nil {
		t.Fatal("expected error")
	}
	if got := rec.LastDaemonRPCOutcome.Load(); got != metrics.OutcomeError {
		t.Fatalf("outcome = %v", got)
	}
}

func TestMeteredClient_ClosePassesThrough(t *testing.T) {
	rec := metrics.NewCounter()
	c := WithMetrics(NewFake(), rec)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// Close is intentionally not metered — lifecycle calls don't show
	// up as RPCs in the daemon-RPC histogram.
	if rec.DaemonRPCCalls.Load() != 0 {
		t.Fatalf("Close should not record an RPC, got %d", rec.DaemonRPCCalls.Load())
	}
}

func TestMeteredClient_NilRecorderReturnsInner(t *testing.T) {
	inner := NewFake()
	if got := WithMetrics(inner, nil); got != inner {
		t.Fatal("nil recorder should return the inner client unchanged")
	}
}

// TestMeteredClient_ObserveCountMatchesIncCount asserts that every
// IncDaemonRPC has a paired ObserveDaemonRPC call (i.e. the wrapper
// invokes both on every code path, success and failure). The Counter
// helper in the metrics package conflates the dual-histogram observe
// into a single ObserveDaemonRPC call, so the assertion is on call
// equality rather than on per-histogram counts.
func TestMeteredClient_ObserveCountMatchesIncCount(t *testing.T) {
	rec := metrics.NewCounter()
	inner := NewFake()
	c := WithMetrics(inner, rec)
	ctx := context.Background()

	_, _ = c.ListCapabilities(ctx)
	_, _ = c.GetQuote(ctx, nil, "x")
	_, _ = c.ProcessPayment(ctx, []byte{1}, "w")
	_, _ = c.DebitBalance(ctx, nil, "w", 0)

	// Trigger an error path too.
	inner.ProcessPaymentError = errors.New("boom")
	_, _ = c.ProcessPayment(ctx, nil, "")

	if rec.DaemonRPCCalls.Load() != rec.DaemonRPCObserves.Load() {
		t.Fatalf("calls=%d observes=%d (must be equal)", rec.DaemonRPCCalls.Load(), rec.DaemonRPCObserves.Load())
	}
	if rec.DaemonRPCCalls.Load() != 5 {
		t.Fatalf("expected 5 RPCs metered, got %d", rec.DaemonRPCCalls.Load())
	}
}
