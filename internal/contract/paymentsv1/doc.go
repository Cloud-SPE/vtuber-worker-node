// Package paymentsv1 is vtuber-worker-node's local snapshot of the
// payment-daemon payee-side gRPC wire contract.
//
// These generated files are intentionally owned in-repo so the worker
// depends on payment-daemon only at the proto/wire level, not as an
// upstream Go module. When the daemon contract changes, refresh the
// proto snapshot under /proto/livepeer/payments/v1 and regenerate or
// replace the stubs here in the same change.
package paymentsv1
