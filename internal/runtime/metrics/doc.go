// Package metrics hosts the worker's HTTP /metrics + /healthz
// listener. Distinct from internal/runtime/http (which serves the
// customer-facing OpenAI API) because:
//
//  1. Prometheus expects pull over TCP HTTP; running it on the same
//     port as the customer surface would leak metrics to anyone with
//     a valid API key.
//  2. The metrics surface has a different trust posture — it's
//     scrape-only, low-sensitivity, and operators want it on a
//     well-known internal port (9093 by default) for their existing
//     scrapers, isolated from customer ingress.
//
// The listener is opt-in: it only runs when --metrics-listen is set.
// When unset, the worker installs a Noop recorder and never opens
// a TCP socket.
package metrics
