package vtuber_session

// Event types and envelope for the WebSocket control plane back to the
// bridge. Vocabulary mirrors livepeer-vtuber-project/docs/design-docs/
// events-taxonomy.md (the OpenAI-Realtime-shaped naming the design
// pinned in ADR-002 / ADR-004).
//
// This package emits a subset of the upstream taxonomy: just the
// events the worker side originates. The session-runner originates
// session.transcript.* / session.expression.* / session.speaking.* /
// session.heartbeat / session.error and forwards them to the bridge
// via this worker; the worker itself only adds session.usage.tick,
// session.balance.low, session.balance.refilled, session.ready,
// session.ended.
//
// Event names are append-only — never rename a fired event without
// an explicit deprecation window. See the upstream events-taxonomy.md
// "Versioning" section.

// EventType is one of the dotted-name strings that appear in the
// `type` field of every outbound event envelope.
type EventType string

const (
	// EventSessionReady is emitted once when the module's Serve has
	// successfully forwarded session-open to the backend and the
	// backend acknowledged readiness.
	EventSessionReady EventType = "session.ready"

	// EventSessionEnded is the last event the module emits for a
	// session — graceful (reason: "user_end") or forced (reason:
	// "balance_exhausted" / "fatal_error").
	EventSessionEnded EventType = "session.ended"

	// EventSessionError is a non-fatal warning (recoverable: true) or
	// fatal failure (recoverable: false). Fatal errors are followed
	// by EventSessionEnded.
	EventSessionError EventType = "session.error"

	// EventSessionUsageTick is emitted every successful Debit. Carries
	// the units consumed since the last tick and the cumulative total
	// for the session.
	EventSessionUsageTick EventType = "session.usage.tick"

	// EventSessionBalanceLow is emitted ONCE per low-balance state
	// transition (not every tick). The bridge translates the units to
	// USD and forwards a customer-visible "please top up" event.
	EventSessionBalanceLow EventType = "session.balance.low"

	// EventSessionBalanceRefilled is emitted when a Sufficient check
	// returns true after having been false. Cancels the bridge-side
	// grace timer.
	EventSessionBalanceRefilled EventType = "session.balance.refilled"
)

// ErrorCode classifies session.error events. Stable strings; the
// bridge parses these to decide whether to retry, surface to the
// customer, or terminate.
type ErrorCode string

const (
	// ErrCodeBalanceExhausted is fatal: a Sufficient call kept
	// returning false past the grace window.
	ErrCodeBalanceExhausted ErrorCode = "balance_exhausted"

	// ErrCodePaymentUnreachable is recoverable (transient): the
	// payment-daemon Debit call failed N times in a row but the grace
	// window hasn't expired yet.
	ErrCodePaymentUnreachable ErrorCode = "payment_unreachable"

	// ErrCodeBackendDown is fatal: the backend (session-runner)
	// HTTP forward failed at session-open or closed unexpectedly
	// mid-session.
	ErrCodeBackendDown ErrorCode = "backend_down"

	// ErrCodeBridgeDisconnect is recoverable initially (the bridge
	// websocket dropped; we may reconnect) and fatal after retry
	// budget is exhausted.
	ErrCodeBridgeDisconnect ErrorCode = "bridge_disconnect"

	// ErrCodeFatal is the catch-all for unanticipated panics and
	// invariant violations. Always recoverable=false.
	ErrCodeFatal ErrorCode = "fatal"
)

// EndReason classifies session.ended events.
type EndReason string

const (
	// EndReasonUserEnd is the graceful close path — bridge sent
	// session.end inbound or the session-runner reported a clean
	// teardown.
	EndReasonUserEnd EndReason = "user_end"

	// EndReasonBalanceExhausted is the forced close path — grace
	// window expired with the balance still low.
	EndReasonBalanceExhausted EndReason = "balance_exhausted"

	// EndReasonFatalError is the catch-all for any session.error
	// with recoverable=false.
	EndReasonFatalError EndReason = "fatal_error"

	// EndReasonShutdown is the worker-shutdown path — ctx.Done()
	// from the parent process while a session was running.
	EndReasonShutdown EndReason = "shutdown"
)

// Event is the envelope shape every outbound event uses. Matches the
// upstream events-taxonomy.md envelope (event_id, type, session_id,
// ts_ms, data). The data field is type-specific and serialized as
// JSON when sent over the wire.
type Event struct {
	EventID   string `json:"event_id"`
	Type      EventType `json:"type"`
	SessionID string `json:"session_id"`
	TsMs      int64  `json:"ts_ms"`
	Data      any    `json:"data,omitempty"`
}

// UsageTickData is the payload of EventSessionUsageTick.
type UsageTickData struct {
	WorkUnits           uint64 `json:"work_units"`
	WorkUnitKind        string `json:"work_unit_kind"`
	CumulativeWorkUnits uint64 `json:"cumulative_work_units"`
}

// BalanceLowData is the payload of EventSessionBalanceLow.
type BalanceLowData struct {
	RunwayUnitsRemaining int64 `json:"runway_units_remaining"`
}

// BalanceRefilledData is the payload of EventSessionBalanceRefilled.
type BalanceRefilledData struct {
	NewBalanceUnits int64 `json:"new_balance_units"`
}

// ErrorData is the payload of EventSessionError.
type ErrorData struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	Recoverable bool      `json:"recoverable"`
}

// EndedData is the payload of EventSessionEnded.
type EndedData struct {
	Reason              EndReason `json:"reason"`
	FinalCumulativeUnits uint64   `json:"final_cumulative_units"`
}
