package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// buildFixtureWithCap builds a mux whose max_concurrent_requests is
// set to cap. The default fixture uses 8; limiter tests want smaller
// numbers to actually exercise the full/empty transitions.
func buildFixtureWithCap(t *testing.T, capN int) *testFixture {
	t.Helper()
	cfg := config.New(config.WorkerSection{
		HTTPListen:            "127.0.0.1:0",
		PaymentDaemonSocket:   "/tmp/fake.sock",
		MaxConcurrentRequests: capN,
	}, []config.CapabilityEntry{{
		Capability: "openai:/v1/chat/completions",
		WorkUnit:   types.WorkUnitToken,
		Offerings: []config.OfferingEntry{
			{ID: "test-model", PricePerWorkUnitWei: "100", BackendURL: "http://backend.local:9000"},
		},
	}})
	payee := payeedaemon.NewFake()
	mod := &fakeModule{
		capability: "openai:/v1/chat/completions",
		path:       "/v1/chat/completions",
		extractModelFn: func(body []byte) (types.ModelID, error) {
			var req struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return "", err
			}
			if req.Model == "" {
				return "", errors.New("missing model field")
			}
			return types.ModelID(req.Model), nil
		},
		estimateFn: func(body []byte, model types.ModelID) (int64, error) {
			return 10, nil
		},
		serveFn: func(ctx context.Context, w nethttp.ResponseWriter, r *nethttp.Request, body []byte, model types.ModelID, backendURL string) (int64, error) {
			w.WriteHeader(nethttp.StatusOK)
			return 10, nil
		},
	}
	mux := NewMux(cfg, payee, nil)
	mux.RegisterPaidRoute(mod)
	return &testFixture{mux: mux, payee: payee, module: mod, cfg: cfg}
}

func TestLimiter_AcceptsUpToCap(t *testing.T) {
	// cap=3; fire 3 requests; all should succeed.
	f := buildFixtureWithCap(t, 3)
	for i := 0; i < 3; i++ {
		rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
		if rr.Code != nethttp.StatusOK {
			t.Errorf("request %d: got status %d, want 200", i, rr.Code)
		}
	}
	if f.mux.InflightPaid() != 0 {
		t.Errorf("inflight after all done: got %d, want 0", f.mux.InflightPaid())
	}
}

func TestLimiter_RejectsAboveCap(t *testing.T) {
	// cap=2; pin 2 slots via a blocking Serve; 3rd request should 503.
	f := buildFixtureWithCap(t, 2)

	released := make(chan struct{})
	started := make(chan struct{}, 2)
	f.module.serveFn = func(ctx context.Context, w nethttp.ResponseWriter, r *nethttp.Request, body []byte, model types.ModelID, backendURL string) (int64, error) {
		started <- struct{}{}
		<-released // block until test releases
		w.WriteHeader(nethttp.StatusOK)
		return 10, nil
	}

	// Spawn 2 requests that will pin the semaphore.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
			if rr.Code != nethttp.StatusOK {
				t.Errorf("pinning request: got status %d, want 200", rr.Code)
			}
		}()
	}

	// Wait for both to be inside Serve (holding the semaphore).
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(released)
			t.Fatal("pinning requests never entered Serve")
		}
	}

	// Semaphore now full. Third request must get 503.
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("over-cap request: got status %d, want 503", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "capacity_exhausted")

	// Release pins; pinning goroutines finish.
	close(released)
	wg.Wait()

	// After drain, inflight should drop to 0.
	// (Tiny sleep to let the defer run. Cheap polling.)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && f.mux.InflightPaid() != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if f.mux.InflightPaid() != 0 {
		t.Errorf("inflight after release: got %d, want 0", f.mux.InflightPaid())
	}
}

func TestLimiter_UnpaidRoutesBypass(t *testing.T) {
	// Even with the semaphore pinned full, /health should always
	// return 200. Health checks must not starve under load.
	f := buildFixtureWithCap(t, 1)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	released := make(chan struct{})
	started := make(chan struct{}, 1)
	f.module.serveFn = func(ctx context.Context, w nethttp.ResponseWriter, r *nethttp.Request, body []byte, model types.ModelID, backendURL string) (int64, error) {
		started <- struct{}{}
		<-released
		w.WriteHeader(nethttp.StatusOK)
		return 10, nil
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	}()
	<-started // confirm the slot is held

	// /health should succeed even though paid semaphore is full.
	req := httptest.NewRequest(nethttp.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusOK {
		t.Errorf("/health while semaphore full: got %d, want 200", rr.Code)
	}
	// Inflight should reflect the pinned paid request.
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got, want := body["inflight"], float64(1); got != want {
		t.Errorf("/health inflight: got %v, want %v", got, want)
	}
	if got, want := body["max_concurrent"], float64(1); got != want {
		t.Errorf("/health max_concurrent: got %v, want %v", got, want)
	}

	close(released)
	wg.Wait()
}

func TestLimiter_NonPositiveCapFallsBackToOne(t *testing.T) {
	// cap=0 would make a zero-cap channel (blocks forever).
	// NewMux should fall back to 1.
	f := buildFixtureWithCap(t, 0)
	if got := f.mux.MaxConcurrentPaid(); got != 1 {
		t.Errorf("cap fallback: got %d, want 1", got)
	}
	// A single request should succeed.
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusOK {
		t.Errorf("single-request with cap fallback: got %d, want 200", rr.Code)
	}
}

func TestMux_MaxConcurrentPaidMirrorsConfig(t *testing.T) {
	f := buildFixtureWithCap(t, 5)
	if got := f.mux.MaxConcurrentPaid(); got != 5 {
		t.Errorf("MaxConcurrentPaid: got %d, want 5", got)
	}
}
