package backendhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
)

// Client is the module-facing surface for an HTTP inference backend.
// The worker's runtime/http layer never calls these directly — only
// capability modules do, inside Serve.
type Client interface {
	// DoJSON posts body to url with Content-Type: application/json
	// and returns the buffered response body along with its status
	// code. Used by non-streaming JSON capabilities (chat when
	// stream=false, embeddings, image generation).
	DoJSON(ctx context.Context, url string, body []byte) (status int, respBody []byte, err error)

	// DoRaw posts body with an operator-supplied Content-Type and
	// returns the buffered response. Used by capabilities whose
	// request shape isn't JSON — notably multipart/form-data for
	// /images/edits and /audio/transcriptions, where the Content-Type
	// must preserve the caller's multipart boundary so the backend
	// can parse the body.
	DoRaw(ctx context.Context, url, contentType string, body []byte) (status int, respBody []byte, err error)

	// DoStream posts body with Content-Type: application/json and
	// Accept: text/event-stream, returning the response status, the
	// backend's response headers, and a reader over the raw response
	// body. Callers are responsible for closing the reader.
	//
	// Response headers let the caller relay the backend's actual
	// Content-Type (audio/mpeg vs audio/wav vs text/event-stream)
	// rather than hardcoding. `headers` is non-nil on success; may
	// be nil on error.
	DoStream(ctx context.Context, url string, body []byte) (status int, headers http.Header, stream io.ReadCloser, err error)
}

// WithMetrics wraps a Client so every call also emits the corresponding
// backend HTTP metrics (request count, latency, error class, last-success
// timestamp). The wrapper is thin and allocation-free per-call.
//
// Constraint: the Client interface methods take only (url, body); they
// do NOT carry capability+model. The decorator therefore captures these
// at construction time. In Pass A the worker has one Client per module,
// and each module already knows its capability + the request's model
// before the call — but routing model through here for streaming SSE
// (where the chosen model is request-scoped) requires the wiring to
// supply (capability, model) per construction site. A future interface
// change that promotes (capability, model) onto the request — e.g.
//
//	type Request struct { URL, ContentType string; Body []byte; Capability, Model string }
//	DoJSON(ctx, Request) (...)
//
// — would let a single decorated Client serve every (capability, model)
// pair without re-wrapping. That change is intentionally deferred to
// Pass B; here we capture per construction.
//
// `capability` and `model` may be empty; the recorder substitutes its
// _unset_ sentinel.
func WithMetrics(c Client, rec metrics.Recorder, capability, model string) Client {
	if rec == nil {
		return c
	}
	return &meteredClient{inner: c, rec: rec, capability: capability, model: model}
}

type meteredClient struct {
	inner      Client
	rec        metrics.Recorder
	capability string
	model      string
}

func (m *meteredClient) DoJSON(ctx context.Context, url string, body []byte) (int, []byte, error) {
	start := time.Now()
	status, resp, err := m.inner.DoJSON(ctx, url, body)
	m.recordOutcome(start, status, err)
	return status, resp, err
}

func (m *meteredClient) DoRaw(ctx context.Context, url, contentType string, body []byte) (int, []byte, error) {
	start := time.Now()
	status, resp, err := m.inner.DoRaw(ctx, url, contentType, body)
	m.recordOutcome(start, status, err)
	return status, resp, err
}

func (m *meteredClient) DoStream(ctx context.Context, url string, body []byte) (int, http.Header, io.ReadCloser, error) {
	start := time.Now()
	status, hdr, stream, err := m.inner.DoStream(ctx, url, body)
	m.recordOutcome(start, status, err)
	return status, hdr, stream, err
}

// recordOutcome emits the IncBackendRequest + ObserveBackendRequest
// pair on every call, plus IncBackendError on transport / 5xx failures
// and SetBackendLastSuccess on a successful 2xx. Status==0 with a
// non-nil err signals a transport failure (no HTTP response received).
func (m *meteredClient) recordOutcome(start time.Time, status int, err error) {
	d := time.Since(start)
	m.rec.ObserveBackendRequest(m.capability, m.model, d)
	switch {
	case err != nil:
		m.rec.IncBackendRequest(m.capability, m.model, metrics.OutcomeError)
		m.rec.IncBackendError(m.capability, m.model, classifyTransportError(err))
	case status >= 500:
		m.rec.IncBackendRequest(m.capability, m.model, metrics.OutcomeError)
		m.rec.IncBackendError(m.capability, m.model, metrics.BackendError5xx)
	default:
		m.rec.IncBackendRequest(m.capability, m.model, metrics.OutcomeOK)
		m.rec.SetBackendLastSuccess(m.capability, m.model, time.Now())
	}
}

// classifyTransportError maps a transport-level failure (no HTTP
// response, or read-body failure) to one of the four error_class label
// values defined in the metrics catalog. The classification is
// substring-based: the production fetchClient wraps every failure with
// `backendhttp: <stage>: <err>`, and the underlying err string from
// net/http and io is well-known.
//
// Order matters: timeout / context.DeadlineExceeded must be checked
// before "connect" — a deadline exceeded on the dial is still a timeout
// from the operator's perspective.
func classifyTransportError(err error) string {
	if err == nil {
		return metrics.BackendErrorConnect
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return metrics.BackendErrorTimeout
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"):
		return metrics.BackendErrorTimeout
	case strings.Contains(s, "read body"), strings.Contains(s, "unexpected eof"), strings.Contains(s, "malformed"):
		return metrics.BackendErrorMalformed
	default:
		// Default to connect for refused / DNS / TLS / "do" failures.
		return metrics.BackendErrorConnect
	}
}
