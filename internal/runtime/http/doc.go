// Package http hosts the HTTP server, the Mux that routes both unpaid
// and paid requests, and the payment middleware that every paid route
// passes through.
//
// The contract to remember:
//
//   - Register(method, path, handler)      → unpaid; no middleware.
//   - RegisterPaidRoute(module)            → wraps in paymentMiddleware.
//
// Core belief #3 ("payment is auth") depends on the ONLY way a paid
// route reaches the HTTP surface being RegisterPaidRoute. The planned
// payment-middleware-check lint enforces this mechanically; until then
// reviewers enforce it manually.
package http
