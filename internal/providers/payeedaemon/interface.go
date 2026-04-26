package payeedaemon

import (
	"context"
	"time"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
)

// Client is the small surface the worker-node needs from the
// livepeer-payment-daemon. Four methods cover the full lifecycle:
//
//   - ListCapabilities at startup for the worker/daemon catalog
//     cross-check.
//   - ProcessPayment + DebitBalance on every paid request.
//   - Close on shutdown.
//
// Implementations are expected to be safe for concurrent use —
// middleware calls ProcessPayment and DebitBalance from many goroutines
// against the same Client.
type Client interface {
	// ListCapabilities returns the daemon's full configured catalog.
	// Called once at worker startup; the worker fails closed if its
	// own shared-YAML parse doesn't byte-match this response.
	ListCapabilities(ctx context.Context) (ListCapabilitiesResult, error)

	// GetQuote returns the daemon's TicketParams + per-model prices
	// for a (sender, capability) pair. The worker's /quote and
	// /quotes HTTP handlers proxy this call through to the bridge so
	// the bridge can refresh its quote cache. NotFound is expected
	// when the operator hasn't configured `capability`.
	GetQuote(ctx context.Context, sender []byte, capability string) (GetQuoteResult, error)

	// ProcessPayment validates a payment blob and credits the sender's
	// balance. The workID identifies the session the credit posts to;
	// typically the worker derives it from the payment (e.g. the
	// RecipientRandHash hex) so a sender + capability pair collapses
	// to a single long-lived session.
	ProcessPayment(ctx context.Context, paymentBytes []byte, workID string) (ProcessPaymentResult, error)

	// DebitBalance subtracts workUnits from the (sender, workID)
	// balance. Returns the new balance; a negative balance means the
	// caller over-debited and must refuse to serve further work on
	// this session.
	DebitBalance(ctx context.Context, sender []byte, workID string, workUnits int64) (DebitBalanceResult, error)

	// SufficientBalance reports whether (sender, workID) has at
	// least minWorkUnits of remaining balance. Cheap; does not modify
	// balance. Used by streaming-session modules between Debit ticks
	// to detect a low-balance state without paying for a debit-then-
	// refund cycle. See livepeer-payment-library/docs/design-docs/
	// streaming-session-pattern.md.
	SufficientBalance(ctx context.Context, sender []byte, workID string, minWorkUnits int64) (SufficientBalanceResult, error)

	// CloseSession releases the daemon's per-(sender, workID) state at
	// session end. Idempotent on the daemon side. Used by streaming-
	// session modules; one-shot Module callers don't need it.
	CloseSession(ctx context.Context, sender []byte, workID string) error

	// Close releases the underlying transport. Calling any other
	// method after Close is undefined.
	Close() error
}

// WithMetrics wraps a Client so every RPC also emits the corresponding
// daemon-RPC metrics. The wrapper is thin and allocation-free per-call —
// the recorder methods are inlined by the compiler when the recorder is
// the Noop type. ObserveDaemonRPC writes to both the coarse and fast
// histograms internally, so a single Observe call covers both.
func WithMetrics(c Client, rec metrics.Recorder) Client {
	if rec == nil {
		return c
	}
	return &meteredClient{inner: c, rec: rec}
}

type meteredClient struct {
	inner Client
	rec   metrics.Recorder
}

func (m *meteredClient) ListCapabilities(ctx context.Context) (ListCapabilitiesResult, error) {
	start := time.Now()
	res, err := m.inner.ListCapabilities(ctx)
	outcome := metrics.OutcomeOK
	if err != nil {
		outcome = metrics.OutcomeError
	}
	m.rec.IncDaemonRPC(metrics.MethodListCapabilities, outcome)
	m.rec.ObserveDaemonRPC(metrics.MethodListCapabilities, outcome, time.Since(start))
	return res, err
}

func (m *meteredClient) GetQuote(ctx context.Context, sender []byte, capability string) (GetQuoteResult, error) {
	start := time.Now()
	res, err := m.inner.GetQuote(ctx, sender, capability)
	outcome := metrics.OutcomeOK
	if err != nil {
		outcome = metrics.OutcomeError
	}
	m.rec.IncDaemonRPC(metrics.MethodGetQuote, outcome)
	m.rec.ObserveDaemonRPC(metrics.MethodGetQuote, outcome, time.Since(start))
	return res, err
}

func (m *meteredClient) ProcessPayment(ctx context.Context, paymentBytes []byte, workID string) (ProcessPaymentResult, error) {
	start := time.Now()
	res, err := m.inner.ProcessPayment(ctx, paymentBytes, workID)
	outcome := metrics.OutcomeOK
	if err != nil {
		outcome = metrics.OutcomeError
	}
	m.rec.IncDaemonRPC(metrics.MethodProcessPayment, outcome)
	m.rec.ObserveDaemonRPC(metrics.MethodProcessPayment, outcome, time.Since(start))
	return res, err
}

func (m *meteredClient) DebitBalance(ctx context.Context, sender []byte, workID string, workUnits int64) (DebitBalanceResult, error) {
	start := time.Now()
	res, err := m.inner.DebitBalance(ctx, sender, workID, workUnits)
	outcome := metrics.OutcomeOK
	if err != nil {
		outcome = metrics.OutcomeError
	}
	m.rec.IncDaemonRPC(metrics.MethodDebitBalance, outcome)
	m.rec.ObserveDaemonRPC(metrics.MethodDebitBalance, outcome, time.Since(start))
	return res, err
}

func (m *meteredClient) SufficientBalance(ctx context.Context, sender []byte, workID string, minWorkUnits int64) (SufficientBalanceResult, error) {
	start := time.Now()
	res, err := m.inner.SufficientBalance(ctx, sender, workID, minWorkUnits)
	outcome := metrics.OutcomeOK
	if err != nil {
		outcome = metrics.OutcomeError
	}
	m.rec.IncDaemonRPC(metrics.MethodSufficientBalance, outcome)
	m.rec.ObserveDaemonRPC(metrics.MethodSufficientBalance, outcome, time.Since(start))
	return res, err
}

func (m *meteredClient) CloseSession(ctx context.Context, sender []byte, workID string) error {
	start := time.Now()
	err := m.inner.CloseSession(ctx, sender, workID)
	outcome := metrics.OutcomeOK
	if err != nil {
		outcome = metrics.OutcomeError
	}
	m.rec.IncDaemonRPC(metrics.MethodCloseSession, outcome)
	m.rec.ObserveDaemonRPC(metrics.MethodCloseSession, outcome, time.Since(start))
	return err
}

// Close passes through; lifecycle calls are not metered.
func (m *meteredClient) Close() error {
	return m.inner.Close()
}
