// Package payeedaemon is the worker-node's gRPC client for the
// livepeer-payment-daemon's PayeeDaemon service. It is the single
// boundary between worker business logic and the payments library's
// generated proto types — service-layer code speaks the domain types
// defined here, never paymentsv1.* directly.
//
// The package exposes a small Client interface covering the three RPCs
// the worker actually uses (ProcessPayment, DebitBalance,
// ListCapabilities) plus a Close() for lifecycle, and returns domain
// types from this package rather than proto types.
//
// This isolation is what lets us:
//   - Unit-test middleware and modules against a fake Client without
//     standing up a gRPC server.
//   - Swap the transport later (different socket path, in-process, etc.)
//     without touching service code.
//   - Enforce the layer rule: modules MUST depend on this interface, not
//     on google.golang.org/grpc or the library's proto package.
package payeedaemon
