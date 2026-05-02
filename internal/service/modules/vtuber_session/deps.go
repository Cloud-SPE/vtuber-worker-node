package vtuber_session

import (
	"context"
	"net/http"
	"time"
)

// Clock is the time interface the module uses for the Debit ticker
// and the grace timer. The system clock is the production
// implementation; contract tests inject a fake-clock with
// deterministic Advance() to drive tick boundaries without sleeping.
type Clock interface {
	// Now returns the current time. Used for event timestamps.
	Now() time.Time

	// Ticker returns a Ticker that fires every d. Ticker.Stop must be
	// called when the consumer is done.
	Ticker(d time.Duration) Ticker

	// After returns a channel that fires once after d (used for the
	// grace timer).
	After(d time.Duration) <-chan time.Time
}

// Ticker mirrors time.Ticker but with a Stop method that disables the
// channel rather than draining it (since fakes may share the channel
// across multiple consumers).
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// SystemClock is the production Clock backed by the real time package.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
func (SystemClock) Ticker(d time.Duration) Ticker {
	t := time.NewTicker(d)
	return &systemTicker{t: t}
}
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time { return s.t.C }
func (s *systemTicker) Stop()               { s.t.Stop() }

// BridgeControlPlane is the worker's outbound interface to the bridge.
// The module emits Events through Send and (in future milestones)
// receives bridge-originated control events via Recv. For M3 the
// system implementation is a stub that errors on use; the contract
// tests inject a fake; the real WebSocket-backed implementation lands
// when vtuber-livepeer-bridge-mvp's session-open contract is finalized.
type BridgeControlPlane interface {
	// Send pushes an outbound event to the bridge over the WebSocket
	// control plane. Returns an error if the connection is closed or
	// the send buffer is full beyond a threshold the implementation
	// chooses.
	Send(ctx context.Context, ev Event) error

	// Recv blocks until the bridge sends an inbound event (e.g.
	// user.chat.send, session.end, session.persona.update). Returns
	// nil + ErrBridgeClosed when the WebSocket closes cleanly. The
	// caller drains in a goroutine for the session lifetime.
	Recv(ctx context.Context) (Event, error)

	// Close closes the WebSocket. Idempotent.
	Close() error
}

// BackendForward is the worker's outbound interface to the local
// backend (session-runner). Forwards session-open POSTs over localhost
// HTTP. The module owns interpretation of the response; for v1 a 202
// from the backend means "session-runner accepted; session is starting"
// and the module emits session.ready.
type BackendForward interface {
	// OpenSession POSTs the session-open request to the backend.
	// req is the inbound HTTP request that arrived at the worker (the
	// module forwards body + relevant headers). backendURL is the
	// already-resolved (capability, model) → URL from worker.yaml.
	OpenSession(ctx context.Context, req *http.Request, backendURL string) error

	// Close instructs the backend to tear down the session. Best-
	// effort: the backend may already be gone.
	Close(ctx context.Context, sessionID string, backendURL string) error
}

// IDGenerator produces unique event_id values for outbound Events.
// The system implementation uses ULID-like monotonic per-session IDs;
// contract tests inject a deterministic counter.
type IDGenerator interface {
	NextID() string
}
