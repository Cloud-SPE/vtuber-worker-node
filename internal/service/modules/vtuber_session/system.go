package vtuber_session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

// ErrBridgeClosed is returned by BridgeControlPlane.Recv when the
// WebSocket has closed cleanly.
var ErrBridgeClosed = errors.New("vtuber_session: bridge control plane closed")

// ErrBridgeNotConfigured is returned by the stub system bridge until
// the WebSocket-backed implementation lands.
var ErrBridgeNotConfigured = errors.New("vtuber_session: system bridge not configured (stub); inject a BridgeControlPlane")

// stubSystemBridge is the placeholder BridgeControlPlane used until
// vtuber-livepeer-bridge-mvp's session-open contract is finalized and
// a real WebSocket-backed implementation lands. Calling any method
// returns ErrBridgeNotConfigured. Production deployments inject a
// real implementation via Config; contract tests inject a fake.
//
// Keeping a stub (vs. a panic-on-use) means the module compiles and
// runs against tests that don't exercise the bridge — useful for
// isolated debit-ticker tests in M4.
type stubSystemBridge struct{}

func (stubSystemBridge) Send(ctx context.Context, ev Event) error {
	return ErrBridgeNotConfigured
}

func (stubSystemBridge) Recv(ctx context.Context) (Event, error) {
	return Event{}, ErrBridgeNotConfigured
}

func (stubSystemBridge) Close() error { return nil }

// NewStubBridge returns a BridgeControlPlane that errors on use.
// The vtuber-livepeer-bridge-mvp build plan replaces this with a
// real WebSocket-backed implementation.
func NewStubBridge() BridgeControlPlane { return stubSystemBridge{} }

// HTTPBackend is the system implementation of BackendForward. POSTs
// session-open to the configured backend URL using the standard
// library net/http.Client. Returns nil on a 2xx response; an error
// otherwise. Body of the inbound request is forwarded verbatim to
// the backend.
type HTTPBackend struct {
	Client *http.Client
}

// NewHTTPBackend returns an HTTPBackend with a sensible default
// client (timeout-less since session-open responses are quick but
// the same client is reused for Close). Pass a custom Client for
// per-deployment timeouts / transport tuning.
func NewHTTPBackend(c *http.Client) *HTTPBackend {
	if c == nil {
		c = http.DefaultClient
	}
	return &HTTPBackend{Client: c}
}

// OpenSession forwards the inbound POST to the backend. The backend
// is expected to return 202 "session starting" — anything else is
// a backend-down error.
func (h *HTTPBackend) OpenSession(ctx context.Context, req *http.Request, backendURL string) error {
	if req == nil {
		return errors.New("vtuber_session: HTTPBackend.OpenSession: nil request")
	}
	if backendURL == "" {
		return errors.New("vtuber_session: HTTPBackend.OpenSession: empty backendURL")
	}
	body := req.Body
	if body == nil {
		body = http.NoBody
	}
	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL, body)
	if err != nil {
		return fmt.Errorf("build backend request: %w", err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		outReq.Header.Set("Content-Type", ct)
	}
	resp, err := h.Client.Do(outReq)
	if err != nil {
		return fmt.Errorf("backend POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read up to 4 KiB of the backend's error body for diagnostics.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("backend returned %d: %s", resp.StatusCode, string(errBody))
	}
	return nil
}

// Close instructs the backend to tear down the session. Best-effort.
func (h *HTTPBackend) Close(ctx context.Context, sessionID string) error {
	// Real shape lands in M7 (local-dev integration with session-runner's
	// /api/sessions/{id}/end). For M3, no-op so the goroutine fan-out
	// can call Close without conditional gating.
	return nil
}

// CounterIDGen is a deterministic IDGenerator for testing and a
// reasonable production default for low-concurrency operators (one
// counter per session-runner instance; collisions impossible within
// a session). Production at scale should swap in a ULID generator.
type CounterIDGen struct {
	prefix  string
	counter atomic.Uint64
}

// NewCounterIDGen returns an IDGenerator that emits "<prefix>_NNN"
// strings starting at 1. Pass a per-session prefix for uniqueness
// across concurrent sessions.
func NewCounterIDGen(prefix string) *CounterIDGen {
	return &CounterIDGen{prefix: prefix}
}

// NextID returns the next ID in the sequence.
func (g *CounterIDGen) NextID() string {
	n := g.counter.Add(1)
	return fmt.Sprintf("%s_%03d", g.prefix, n)
}
