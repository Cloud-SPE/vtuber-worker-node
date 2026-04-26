package modules

import (
	"context"
	"net/http"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// Module is the contract between a capability implementation and the
// runtime/http mux. One Module = one capability (e.g. the chat
// completions module covers every chat model the operator configured).
//
// Layer rule: implementations MUST NOT import payeedaemon, config, or
// any cross-cutting concern outside providers/. The middleware is the
// seam between payments and modules.
type Module interface {
	// Capability returns the canonical capability string this module
	// serves (e.g. "openai:/v1/chat/completions").
	Capability() types.CapabilityID

	// HTTPMethod is the HTTP verb the mux registers. Always "POST" for
	// OpenAI-shaped capabilities; kept as an interface method so
	// future non-OpenAI modules (transcoding, custom) can declare
	// their own.
	HTTPMethod() string

	// HTTPPath is the URI path the mux registers (e.g.
	// "/v1/chat/completions"). Must be stable across the module's
	// lifetime — the mux registers once at startup.
	HTTPPath() string

	// Unit returns the work-unit dimension this capability meters in.
	// One of: "token", "character", "audio_second",
	// "image_step_megapixel" — see metrics conventions. The middleware
	// uses this string when emitting `work_units_total` after a
	// successful response so the module stays the single source of
	// truth for its own metering dimension.
	Unit() string

	// ExtractModel inspects the request body and returns the model the
	// request is addressed to. The mux uses this to resolve
	// (capability, model) → backend URL before DebitBalance. Returns
	// an error if the body is malformed or model missing.
	ExtractModel(body []byte) (types.ModelID, error)

	// EstimateWorkUnits computes the upfront debit amount for this
	// request. The model parameter is passed in so modules with per-
	// model metering (different tokenizers, different image-step
	// multipliers) can branch. Conservative overestimation is fine —
	// the worker operates under an over-debit-accepted policy.
	EstimateWorkUnits(body []byte, model types.ModelID) (int64, error)

	// Serve dispatches the (already-payment-validated) request to the
	// backend inference server, streams the response to w, and returns
	// the actual work units consumed. The middleware uses the return
	// value for reconciliation (over-debit if actual > estimate;
	// no-op if actual < estimate).
	//
	// backendURL is the resolved target for (capability, model) from
	// the worker config. The module is responsible for forming the
	// backend request.
	Serve(
		ctx context.Context,
		w http.ResponseWriter,
		r *http.Request,
		body []byte,
		model types.ModelID,
		backendURL string,
	) (actualUnits int64, err error)
}
