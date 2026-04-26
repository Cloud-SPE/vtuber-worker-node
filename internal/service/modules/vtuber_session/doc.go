// Package vtuber_session implements the StreamingModule for the
// `livepeer:vtuber-session` capability.
//
// The module owns one streaming session's lifetime: it opens a
// WebSocket back to the bridge for control-plane events, forwards
// session-open to its local backend (session-runner over localhost
// HTTP), runs a 5s-cadence Debit ticker, and emits session.balance.low
// when the underlying PaymentSession reports insufficient runway.
// On every termination path (graceful close, balance-exhausted,
// fatal error, panic) the module calls PaymentSession.Close exactly
// once before Serve returns.
//
// Canonical reference (operational design):
//
//	livepeer-vtuber-project/docs/design-docs/streaming-session-module.md
//
// The module is dependency-injected via Config so contract tests can
// substitute a fake Clock, fake BridgeControlPlane, fake BackendForward,
// and fake PaymentSession. See module_test.go for the test fixtures
// and the five contract tests pinned by the bootstrap plan's M4.
package vtuber_session
