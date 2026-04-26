package backendhttp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
)

// Fake is an in-memory Client for tests. Programmable response shapes
// for both DoJSON (buffered) and DoStream (SSE chunks). Records the
// last request body seen so tests can assert the worker passed the
// bridge's bytes through verbatim.
//
// Fake is concurrency-safe.
type Fake struct {
	mu sync.Mutex

	// For DoJSON + DoRaw (shared — DoJSON is just DoRaw with a
	// fixed Content-Type, so tests that assert one also apply to
	// the other).
	JSONStatus int
	JSONBody   []byte
	JSONErr    error

	// For DoRaw specifically: LastRawContentType captures what the
	// caller passed, so tests can assert multipart boundary
	// preservation.
	LastRawContentType string
	RawCalls           int

	// For DoStream: StreamChunks is written one-per-chunk (each chunk
	// already includes the `data: ...\n\n` framing, or the final
	// `data: [DONE]\n\n` marker). Kept raw so tests can insert exactly
	// what they want the module to see.
	StreamStatus  int
	StreamHeaders http.Header
	StreamChunks  [][]byte
	StreamErr     error

	// Observations recorded on each call.
	LastJSONURL    string
	LastJSONBody   []byte
	LastStreamURL  string
	LastStreamBody []byte
	JSONCalls      int
	StreamCalls    int
}

func NewFake() *Fake {
	return &Fake{
		JSONStatus:   200,
		StreamStatus: 200,
	}
}

func (f *Fake) DoJSON(ctx context.Context, url string, body []byte) (int, []byte, error) {
	return f.DoRaw(ctx, url, "application/json", body)
}

func (f *Fake) DoRaw(_ context.Context, url, contentType string, body []byte) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.JSONCalls++
	f.RawCalls++
	f.LastJSONURL = url
	f.LastJSONBody = append([]byte(nil), body...)
	f.LastRawContentType = contentType
	if f.JSONErr != nil {
		return 0, nil, f.JSONErr
	}
	return f.JSONStatus, append([]byte(nil), f.JSONBody...), nil
}

func (f *Fake) DoStream(_ context.Context, url string, body []byte) (int, http.Header, io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StreamCalls++
	f.LastStreamURL = url
	f.LastStreamBody = append([]byte(nil), body...)
	if f.StreamErr != nil {
		return 0, nil, nil, f.StreamErr
	}
	buf := bytes.NewBuffer(nil)
	for _, c := range f.StreamChunks {
		buf.Write(c)
	}
	headers := f.StreamHeaders
	if headers == nil {
		headers = http.Header{}
	}
	return f.StreamStatus, headers.Clone(), io.NopCloser(buf), nil
}
