// Package config loads and projects worker.yaml into the worker-specific
// form. The worker owns this parser and validation logic directly so it
// consumes payment-daemon as a contract, not as an imported source
// library, while still enforcing the shared YAML invariants it relies
// on. The result is a Config with a flat (capability, offering)-indexed
// map over the capabilities block for O(1) routing.
//
// It also owns the daemon-consistency cross-check: once the worker
// dials the payee daemon and pulls ListCapabilities, call
// VerifyDaemonCatalog to assert byte-equality against the worker's
// own parse. Mismatch is fail-closed.
package config
