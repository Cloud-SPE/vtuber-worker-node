// Command payment-middleware-check enforces core belief #3:
// "Payment is authentication." Every paid HTTP route MUST pass through
// runtime/http.Mux.RegisterPaidRoute; registering a capability path
// via Mux.Register (unpaid) is a security bug.
//
// The check is deliberately simple and conservative:
//
//  1. Walk every .go file under --root.
//  2. For each call expression matching `<x>.Register(a, b, c)` with
//     exactly three arguments, look at the second argument.
//  3. If the second argument is a string literal that starts with
//     "/v1/", flag it. Such paths are capability routes per the
//     worker's HTTP contract (docs/product-specs/index.md) and MUST
//     be registered via RegisterPaidRoute.
//
// False positives are possible if a third-party library happens to
// expose a Register(method, path, handler) method used with a "/v1/..."
// literal; suppress those with a `//nolint:paymentmiddleware` comment
// on the same line (convention, not yet enforced automatically).
//
// The checker intentionally doesn't try to resolve types. That would
// require loading the whole program and would slow the lint cycle for
// marginal benefit — the signature match + path-prefix heuristic
// catches the failure mode the worker actually cares about.
package main
