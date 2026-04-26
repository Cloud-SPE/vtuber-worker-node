package metrics

import (
	"net/http"
	"time"
)

// Recorder is the single metrics surface. Every domain emits through
// it; implementations decide how to record. Two implementations:
//
//   - Prometheus (production): writes to a Prometheus *Registry,
//     enforces a cardinality cap per metric, exposes the standard
//     /metrics handler.
//   - Noop (default when --metrics-listen is unset): zero-cost no-op,
//     Handler returns 404.
//
// Method ordering follows the catalog in docs/design-docs/metrics.md.
// New emissions add a method here and wire it in every implementation.
type Recorder interface {
	// ----- Request lifecycle (customer-facing HTTP surface) -----

	// IncRequest counts one completed request, labeled by capability,
	// model, and outcome. outcome ∈ {2xx, 4xx, 402, 5xx, canceled}.
	// Empty labels are emitted as "_unset_" to keep Prom output clean.
	IncRequest(capability, model, outcome string)
	// ObserveRequest records the end-to-end request latency including
	// payment validation, semaphore acquire, backend round-trip, and
	// reconcile. Single histogram, default Prom buckets.
	ObserveRequest(capability, model, outcome string, d time.Duration)

	// ----- Work units (the revenue signal) -----

	// AddWorkUnits increments the work_units_total counter by n. unit
	// ∈ {token, character, audio_second, image_step_megapixel}. This
	// is the single most important metric in the worker — the bridge
	// joins it against revenue for margin reconciliation.
	AddWorkUnits(capability, model, unit string, n int64)

	// ----- Daemon RPC (unix-socket gRPC; dual-histogram) -----

	// IncDaemonRPC counts one completed payee-daemon RPC. method ∈
	// {ProcessPayment, DebitBalance, GetQuote, ListCapabilities}.
	IncDaemonRPC(method, outcome string)
	// ObserveDaemonRPC records the unary RPC latency. Two histograms
	// fire: a coarse-grained one (default Prom buckets) and a sub-ms
	// fast variant for the unix-socket fast path.
	ObserveDaemonRPC(method, outcome string, d time.Duration)

	// ----- Backend HTTP (over-the-wire upstream model server) -----

	// IncBackendRequest counts one completed backend HTTP call.
	// outcome ∈ {ok, error}.
	IncBackendRequest(capability, model, outcome string)
	// ObserveBackendRequest records the backend round-trip latency.
	ObserveBackendRequest(capability, model string, d time.Duration)
	// IncBackendError counts one classified backend error. errorClass
	// ∈ {timeout, 5xx, malformed, connect}.
	IncBackendError(capability, model, errorClass string)
	// SetBackendLastSuccess records the unix timestamp of the most
	// recent successful backend response for (capability, model).
	// Operators alert on staleness here.
	SetBackendLastSuccess(capability, model string, t time.Time)

	// ----- Tokenizer -----

	// IncTokenizerCall counts one tokenizer estimate. outcome ∈
	// {ok, fallback, error}. Latency intentionally skipped — the
	// tiktoken impl is typically <100µs.
	IncTokenizerCall(model, outcome string)

	// ----- Capacity & shedding -----

	// IncCapacityRejection counts a 503 emitted because the inflight
	// semaphore was full.
	IncCapacityRejection(capability string)
	// SetInflightRequests reports the live semaphore depth.
	SetInflightRequests(n int)

	// ----- Payment validation -----

	// IncPaymentRejection counts one 402 emitted by paymentMiddleware.
	// reason ∈ {process_payment_failed, insufficient_balance,
	// debit_error, header_invalid, header_missing}.
	IncPaymentRejection(reason string)

	// ----- Build/health -----

	SetUptimeSeconds(s float64)
	SetBuildInfo(version, protocolVersion, goVersion string)
	SetMaxConcurrent(n int)

	// ----- Exposition -----

	// Handler returns the http.Handler that serves the Prometheus
	// exposition format on the metrics listener. For Noop it returns
	// 404, so an operator who forgets `--metrics-listen` and points
	// Prometheus at a stale port gets a clear "no metrics here"
	// signal rather than a successful empty scrape.
	Handler() http.Handler
}

// Sentinels for label values. Use these constants instead of string
// literals at call sites so a typo fails to compile.
const (
	// outcome — request lifecycle
	OutcomeRequest2xx      = "2xx"
	OutcomeRequest4xx      = "4xx"
	OutcomeRequest402      = "402"
	OutcomeRequest5xx      = "5xx"
	OutcomeRequestCanceled = "canceled"

	// outcome — daemon RPC, backend HTTP, tokenizer
	OutcomeOK       = "ok"
	OutcomeError    = "error"
	OutcomeFallback = "fallback"

	// unit — work units (must match conventions doc + bridge)
	UnitToken              = "token"
	UnitCharacter          = "character"
	UnitAudioSecond        = "audio_second"
	UnitImageStepMegapixel = "image_step_megapixel"

	// method — daemon RPC
	MethodProcessPayment    = "ProcessPayment"
	MethodDebitBalance      = "DebitBalance"
	MethodSufficientBalance = "SufficientBalance"
	MethodCloseSession      = "CloseSession"
	MethodGetQuote          = "GetQuote"
	MethodListCapabilities  = "ListCapabilities"

	// unit — streaming session work
	UnitSecond = "second"

	// error_class — backend HTTP
	BackendErrorTimeout   = "timeout"
	BackendError5xx       = "5xx"
	BackendErrorMalformed = "malformed"
	BackendErrorConnect   = "connect"

	// reason — payment rejection
	PaymentRejectProcessPayment      = "process_payment_failed"
	PaymentRejectInsufficientBalance = "insufficient_balance"
	PaymentRejectDebitError          = "debit_error"
	PaymentRejectHeaderInvalid       = "header_invalid"
	PaymentRejectHeaderMissing       = "header_missing"

	// label fallback for unset values
	LabelUnset = "_unset_"
)
