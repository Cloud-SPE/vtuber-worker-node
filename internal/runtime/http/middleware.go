package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"time"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/service/modules"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// maxPaidRequestBodyBytes is the absolute cap on a paid route body.
// Chat / embeddings rarely exceed 1 MiB; we pick a generous default
// and will let specific modules lower it via their own validation
// when called for.
const maxPaidRequestBodyBytes = 16 << 20 // 16 MiB

// paidRouteDeps is the dependency bundle the middleware closes over.
// Fields are set once at registration and read-only thereafter.
type paidRouteDeps struct {
	module modules.Module
	cfg    *config.Config
	payee  payeedaemon.Client
	// sem is the Mux's paid-route semaphore. Buffered channel;
	// cap = worker.max_concurrent_requests, len = in-flight count.
	// Middleware attempts a non-blocking send on entry.
	sem      chan struct{}
	logger   *slog.Logger
	recorder metrics.Recorder
}

// paymentMiddleware is the canonical paid-request pipeline. Every
// paid route MUST pass through this function; RegisterPaidRoute is
// the only public surface that builds a handler with it wired in.
//
// Flow:
//
//  1. Parse body (bounded).
//  2. Extract + base64-decode the `livepeer-payment` header.
//  3. Derive work_id from the payment bytes.
//  4. ProcessPayment → { sender, credited_ev, balance, winners }.
//  5. Module extracts model; lookup (capability, model) → backend URL.
//  6. EstimateWorkUnits(body, model) → int64.
//  7. DebitBalance(sender, work_id, estimate); reject if balance < 0.
//  8. Module.Serve(...) returns actual work units consumed.
//  9. Reconcile: if actual > estimate, second DebitBalance(delta).
//
// Errors along the way map to the contract documented in
// docs/product-specs/index.md: 402 / 404 / 502 / 503 / 400.
func paymentMiddleware(deps paidRouteDeps) http.HandlerFunc {
	rec := deps.recorder
	if rec == nil {
		rec = metrics.NewNoop()
	}
	capLabel := string(deps.module.Capability())
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// Wrap the response writer so we can observe the final status
		// code without the module having to report it.
		sw := &statusWriter{ResponseWriter: w, status: 0}

		// 0. Concurrency gate. Non-blocking: if no slot is free we
		//    immediately return 503 rather than queuing — per the
		//    architecture, queueing adds tail-latency debt we don't
		//    want. Unpaid routes bypass this entirely.
		if deps.sem != nil {
			select {
			case deps.sem <- struct{}{}:
				rec.SetInflightRequests(len(deps.sem))
				defer func() {
					<-deps.sem
					rec.SetInflightRequests(len(deps.sem))
				}()
			default:
				rec.IncCapacityRejection(capLabel)
				writeJSONError(sw, http.StatusServiceUnavailable, "capacity_exhausted", "worker at max_concurrent_requests — retry after in-flight requests drain")
				rec.IncRequest(capLabel, metrics.LabelUnset, metrics.OutcomeRequest5xx)
				rec.ObserveRequest(capLabel, metrics.LabelUnset, metrics.OutcomeRequest5xx, time.Since(start))
				return
			}
		}

		ctx := r.Context()
		// modelLabel is filled in once we've extracted it; before that
		// we use the recorder's _unset_ sentinel.
		modelLabel := metrics.LabelUnset

		// On every exit path emit IncRequest + ObserveRequest with the
		// final outcome bucket. Defers run LIFO; this runs after
		// Serve has returned and after the semaphore release defer.
		defer func() {
			outcome := classifyOutcome(ctx, sw.status)
			rec.IncRequest(capLabel, modelLabel, outcome)
			rec.ObserveRequest(capLabel, modelLabel, outcome, time.Since(start))
		}()

		// 1. Body.
		body, err := io.ReadAll(http.MaxBytesReader(sw, r.Body, maxPaidRequestBodyBytes))
		if err != nil {
			writeJSONError(sw, http.StatusBadRequest, "invalid_request", "failed to read request body: "+err.Error())
			return
		}

		// 2. Payment header.
		hdr := r.Header.Get(types.PaymentHeaderName)
		if hdr == "" {
			rec.IncPaymentRejection(metrics.PaymentRejectHeaderMissing)
			writeJSONError(sw, http.StatusPaymentRequired, "missing_or_invalid_payment", "missing "+types.PaymentHeaderName+" header")
			return
		}
		paymentBytes, err := base64.StdEncoding.DecodeString(hdr)
		if err != nil {
			rec.IncPaymentRejection(metrics.PaymentRejectHeaderInvalid)
			writeJSONError(sw, http.StatusPaymentRequired, "missing_or_invalid_payment", "header is not valid base64")
			return
		}

		// 3. work_id.
		workID := deriveWorkID(paymentBytes)

		// 4. ProcessPayment.
		pp, err := deps.payee.ProcessPayment(ctx, paymentBytes, string(workID))
		if err != nil {
			deps.logger.Warn("ProcessPayment rejected",
				"capability", deps.module.Capability(),
				"work_id", workID,
				"err", err)
			rec.IncPaymentRejection(metrics.PaymentRejectProcessPayment)
			writeJSONError(sw, http.StatusPaymentRequired, "payment_rejected", err.Error())
			return
		}

		// 5. Extract model + resolve route.
		model, err := deps.module.ExtractModel(body)
		if err != nil {
			writeJSONError(sw, http.StatusBadRequest, "invalid_request", "could not extract model from request body: "+err.Error())
			return
		}
		modelLabel = string(model)
		route, ok := deps.cfg.Lookup(deps.module.Capability(), model)
		if !ok {
			writeJSONError(sw, http.StatusNotFound, "capability_not_found", "no backend configured for capability="+string(deps.module.Capability())+" model="+string(model))
			return
		}

		// 6. Upfront estimate.
		estimate, err := deps.module.EstimateWorkUnits(body, model)
		if err != nil {
			writeJSONError(sw, http.StatusBadRequest, "invalid_request", "could not estimate work units: "+err.Error())
			return
		}
		if estimate < 0 {
			estimate = 0
		}

		// 7. DebitBalance upfront.
		db, err := deps.payee.DebitBalance(ctx, pp.Sender, string(workID), estimate)
		if err != nil {
			rec.IncPaymentRejection(metrics.PaymentRejectDebitError)
			writeJSONError(sw, http.StatusBadGateway, "backend_unavailable", "DebitBalance: "+err.Error())
			return
		}
		if db.BalanceWei.Sign() < 0 {
			deps.logger.Info("insufficient balance after estimate debit",
				"capability", deps.module.Capability(),
				"model", model,
				"estimate_work_units", estimate,
				"balance_wei", db.BalanceWei.String())
			rec.IncPaymentRejection(metrics.PaymentRejectInsufficientBalance)
			writeJSONError(sw, http.StatusPaymentRequired, "insufficient_balance", "balance after estimate debit is negative")
			return
		}

		// 8. Serve.
		actual, err := deps.module.Serve(ctx, sw, r, body, model, route.BackendURL)
		if err != nil {
			// Module handlers may partially write the body before
			// erroring. Log and return — we can't replace headers that
			// are already on the wire.
			deps.logger.Warn("module.Serve error",
				"capability", deps.module.Capability(),
				"model", model,
				"err", err)
			return
		}

		// Emit work units after a successful response. Use the
		// module's declared unit dimension so the bridge can join
		// against revenue per (capability, model, unit). The actual
		// count is what the module returned; estimate is irrelevant
		// to the revenue signal — over-debit was already recorded by
		// the daemon.
		if actual > 0 {
			rec.AddWorkUnits(capLabel, string(model), deps.module.Unit(), actual)
		}

		// 9. Reconcile over-debit.
		if actual > estimate {
			delta := actual - estimate
			if _, err := deps.payee.DebitBalance(ctx, pp.Sender, string(workID), delta); err != nil {
				// Logged, not surfaced — the response is already sent.
				deps.logger.Warn("reconcile DebitBalance error",
					"capability", deps.module.Capability(),
					"model", model,
					"delta_work_units", delta,
					"err", err)
			}
		}
	}
}

// statusWriter wraps http.ResponseWriter so the middleware can observe
// the eventual response status. WriteHeader is only called once by the
// downstream handler; subsequent Writes without an explicit
// WriteHeader default to 200 per stdlib semantics, so we mirror that
// in the .status accessor.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the inner ResponseWriter when it implements
// http.Flusher; needed for SSE streaming in chat_completions.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// classifyOutcome maps the final status + ctx error to one of the
// outcome buckets defined in docs/design-docs/metrics.md. The
// canceled bucket fires when the request context was cancelled or its
// deadline expired — a client disconnect mid-stream lands here.
func classifyOutcome(ctx context.Context, status int) string {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return metrics.OutcomeRequestCanceled
		}
	}
	switch {
	case status >= 200 && status < 300:
		return metrics.OutcomeRequest2xx
	case status == 402:
		return metrics.OutcomeRequest402
	case status == 429:
		// 429 isn't a documented bucket — it currently rolls into
		// 4xx in the Phase 1 outcome catalog. Spelled out here so
		// future Phase 2 expansion has an obvious seam.
		return metrics.OutcomeRequest4xx
	case status >= 400 && status < 500:
		return metrics.OutcomeRequest4xx
	case status >= 500 && status < 600:
		return metrics.OutcomeRequest5xx
	case status == 0:
		// No status was ever written — the handler returned early
		// without writing. Treat as 5xx so it shows up in error
		// dashboards rather than disappearing.
		return metrics.OutcomeRequest5xx
	default:
		return metrics.OutcomeRequest5xx
	}
}

// writeJSONError serialises the worker's error contract shape.
// Central helper so every error surface matches
// docs/product-specs/index.md.
func writeJSONError(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  code,
		"detail": detail,
	})
}

// ensureBigIntImported keeps math/big in the import set for the balance
// comparison path; dropped by the linter if unused. Placeholder while
// middleware grows.
var _ = new(big.Int)
