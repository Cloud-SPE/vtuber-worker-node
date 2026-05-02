package backendhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// fetchClient is the production Client backed by stdlib net/http.
// Constructed via NewFetch. Requests pick up the caller's context
// (deadline + cancel).
type fetchClient struct {
	inner *http.Client
}

// NewFetch returns a fetchClient with a reasonable default timeout for
// non-streaming calls. Streaming calls run under the caller's context;
// the client timeout does not apply to them (set to 0 internally so
// long-running SSE streams aren't killed mid-flight).
func NewFetch() Client {
	return &fetchClient{
		inner: &http.Client{
			// Per-request timeouts for non-streaming are enforced via
			// ctx at the call site; the Client.Timeout would also
			// kill streaming connections. Left at 0 (no global cap).
			Timeout: 0,
			Transport: &http.Transport{
				// Keep-alives are important: chat completion traffic is
				// typically chatty and re-dialing TLS each request is
				// wasted latency. These mirror Go defaults but are
				// set explicitly so future tuning is centralized.
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (c *fetchClient) DoJSON(ctx context.Context, url string, body []byte) (int, []byte, error) {
	return c.DoRaw(ctx, url, "application/json", body)
}

func (c *fetchClient) DoRaw(ctx context.Context, url, contentType string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("backendhttp: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.inner.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("backendhttp: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("backendhttp: read body: %w", err)
	}
	return resp.StatusCode, buf, nil
}

func (c *fetchClient) DoStream(ctx context.Context, url string, body []byte) (int, http.Header, io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("backendhttp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.inner.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("backendhttp: do: %w", err)
	}
	// Non-2xx on a stream call: consume + close the body, return
	// status + headers; the module decides how to map to an HTTP
	// error (bridge sees the backend's own error body).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, resp.Header, io.NopCloser(bytes.NewReader(buf)), nil
	}
	return resp.StatusCode, resp.Header, resp.Body, nil
}
