// Package config loads and projects the shared worker.yaml into the
// worker-specific form. It wraps livepeer-payment-library's
// config/sharedyaml package — strict parse + validate — and produces
// a Config that models only the pieces the worker actually uses,
// with a flat (capability, model)-indexed map over the capabilities
// block for O(1) routing.
//
// It also owns the daemon-consistency cross-check: once the worker
// dials the payee daemon and pulls ListCapabilities, call
// VerifyDaemonCatalog to assert byte-equality against the worker's
// own parse. Mismatch is fail-closed.
package config
