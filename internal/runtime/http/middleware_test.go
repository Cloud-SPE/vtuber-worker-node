package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cloud-SPE/livepeer-payment-library/config/sharedyaml"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// fakeModule is a minimal Module for middleware tests.
type fakeModule struct {
	capability     types.CapabilityID
	path           string
	unit           string
	extractModelFn func(body []byte) (types.ModelID, error)
	estimateFn     func(body []byte, model types.ModelID) (int64, error)
	serveFn        func(ctx context.Context, w nethttp.ResponseWriter, r *nethttp.Request, body []byte, model types.ModelID, backendURL string) (int64, error)

	// observed on Serve
	servedBackend atomic.Value // string
}

func (f *fakeModule) Capability() types.CapabilityID { return f.capability }
func (f *fakeModule) HTTPMethod() string             { return nethttp.MethodPost }
func (f *fakeModule) HTTPPath() string               { return f.path }
func (f *fakeModule) Unit() string {
	if f.unit == "" {
		return "token"
	}
	return f.unit
}
func (f *fakeModule) ExtractModel(body []byte) (types.ModelID, error) {
	return f.extractModelFn(body)
}
func (f *fakeModule) EstimateWorkUnits(body []byte, model types.ModelID) (int64, error) {
	return f.estimateFn(body, model)
}
func (f *fakeModule) Serve(
	ctx context.Context,
	w nethttp.ResponseWriter,
	r *nethttp.Request,
	body []byte,
	model types.ModelID,
	backendURL string,
) (int64, error) {
	f.servedBackend.Store(backendURL)
	return f.serveFn(ctx, w, r, body, model, backendURL)
}

// testFixture builds a fully-wired Mux with a single fake module, plus
// a fake payee-daemon and a config with one capability / one model.
type testFixture struct {
	mux    *Mux
	payee  *payeedaemon.Fake
	module *fakeModule
	cfg    *config.Config
}

func buildFixture(t *testing.T) *testFixture {
	t.Helper()
	shared := &sharedyaml.Config{
		ProtocolVersion: sharedyaml.CurrentProtocolVersion,
		Worker: sharedyaml.WorkerConfig{
			HTTPListen:            "127.0.0.1:0",
			PaymentDaemonSocket:   "/tmp/fake.sock",
			MaxConcurrentRequests: 8,
		},
		Capabilities: []sharedyaml.CapabilityConfig{
			{
				Capability: "openai:/v1/chat/completions",
				WorkUnit:   "token",
				Models: []sharedyaml.ModelConfig{
					{Model: "test-model", PricePerWorkUnitWei: "100", BackendURL: "http://backend.local:9000"},
				},
			},
		},
	}
	cfg, err := config.FromShared(shared)
	if err != nil {
		t.Fatalf("FromShared: %v", err)
	}
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"model": string(model), "backend": backendURL})
			return 10, nil
		},
	}
	mux := NewMux(cfg, payee, nil)
	mux.RegisterPaidRoute(mod)
	return &testFixture{mux: mux, payee: payee, module: mod, cfg: cfg}
}

func doPaidRequest(t *testing.T, f *testFixture, body, paymentHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(nethttp.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	if paymentHeader != "" {
		req.Header.Set(types.PaymentHeaderName, paymentHeader)
	}
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	return rr
}

func validHeader() string {
	return base64.StdEncoding.EncodeToString([]byte("fake-payment-bytes"))
}

func TestPaymentMiddleware_HappyPath(t *testing.T) {
	f := buildFixture(t)
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if f.payee.ProcessPaymentCalls != 1 {
		t.Errorf("ProcessPayment calls: got %d, want 1", f.payee.ProcessPaymentCalls)
	}
	if f.payee.DebitBalanceCalls != 1 {
		t.Errorf("DebitBalance calls: got %d, want 1 (no reconcile when actual==estimate)", f.payee.DebitBalanceCalls)
	}
	if f.payee.LastDebitBalanceWorkUnits != 10 {
		t.Errorf("debit work_units: got %d, want 10", f.payee.LastDebitBalanceWorkUnits)
	}
	if got, _ := f.module.servedBackend.Load().(string); got != "http://backend.local:9000" {
		t.Errorf("served backend: got %q, want http://backend.local:9000", got)
	}
}

func TestPaymentMiddleware_MissingHeader(t *testing.T) {
	f := buildFixture(t)
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, "")
	if rr.Code != nethttp.StatusPaymentRequired {
		t.Fatalf("status: got %d, want 402", rr.Code)
	}
	if f.payee.ProcessPaymentCalls != 0 {
		t.Errorf("ProcessPayment should not be called; got %d", f.payee.ProcessPaymentCalls)
	}
	assertErrorCode(t, rr.Body.Bytes(), "missing_or_invalid_payment")
}

func TestPaymentMiddleware_BadBase64(t *testing.T) {
	f := buildFixture(t)
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, "not!base64")
	if rr.Code != nethttp.StatusPaymentRequired {
		t.Fatalf("status: got %d, want 402", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "missing_or_invalid_payment")
}

func TestPaymentMiddleware_ProcessPaymentRejected(t *testing.T) {
	f := buildFixture(t)
	f.payee.ProcessPaymentError = errors.New("unknown sender")
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusPaymentRequired {
		t.Fatalf("status: got %d, want 402", rr.Code)
	}
	if f.payee.DebitBalanceCalls != 0 {
		t.Errorf("DebitBalance should not be called after ProcessPayment failure")
	}
	assertErrorCode(t, rr.Body.Bytes(), "payment_rejected")
}

func TestPaymentMiddleware_ModelExtractionFails(t *testing.T) {
	f := buildFixture(t)
	rr := doPaidRequest(t, f, `not-json`, validHeader())
	if rr.Code != nethttp.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "invalid_request")
	if f.payee.DebitBalanceCalls != 0 {
		t.Errorf("DebitBalance should not be called before route is resolved")
	}
}

func TestPaymentMiddleware_UnknownModel(t *testing.T) {
	f := buildFixture(t)
	rr := doPaidRequest(t, f, `{"model":"not-a-configured-model"}`, validHeader())
	if rr.Code != nethttp.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "capability_not_found")
	if f.payee.DebitBalanceCalls != 0 {
		t.Errorf("DebitBalance should not be called for unknown route")
	}
}

func TestPaymentMiddleware_EstimateError(t *testing.T) {
	f := buildFixture(t)
	f.module.estimateFn = func(body []byte, model types.ModelID) (int64, error) {
		return 0, errors.New("estimator broken")
	}
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "invalid_request")
	if f.payee.DebitBalanceCalls != 0 {
		t.Errorf("DebitBalance must not run when estimate errors")
	}
}

func TestPaymentMiddleware_DebitError(t *testing.T) {
	f := buildFixture(t)
	f.payee.DebitBalanceError = errors.New("daemon hung up")
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "backend_unavailable")
}

func TestPaymentMiddleware_InsufficientBalance(t *testing.T) {
	f := buildFixture(t)
	// Credit is 0, so estimate-debit 10 → balance -10 → fail.
	f.payee.CreditPerCall.SetInt64(0)
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusPaymentRequired {
		t.Fatalf("status: got %d, want 402", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "insufficient_balance")
}

func TestPaymentMiddleware_ReconcilesWhenActualExceedsEstimate(t *testing.T) {
	f := buildFixture(t)
	f.module.serveFn = func(ctx context.Context, w nethttp.ResponseWriter, r *nethttp.Request, body []byte, model types.ModelID, backendURL string) (int64, error) {
		w.WriteHeader(nethttp.StatusOK)
		return 25, nil // actual > estimate (10)
	}
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if f.payee.DebitBalanceCalls != 2 {
		t.Errorf("DebitBalance calls: got %d, want 2 (estimate + reconcile)", f.payee.DebitBalanceCalls)
	}
	// Final debit should be the delta = 25 - 10 = 15.
	if f.payee.LastDebitBalanceWorkUnits != 15 {
		t.Errorf("reconcile debit: got %d work units, want 15", f.payee.LastDebitBalanceWorkUnits)
	}
}

func TestPaymentMiddleware_NoReconcileWhenActualLessThanEstimate(t *testing.T) {
	f := buildFixture(t)
	f.module.serveFn = func(ctx context.Context, w nethttp.ResponseWriter, r *nethttp.Request, body []byte, model types.ModelID, backendURL string) (int64, error) {
		w.WriteHeader(nethttp.StatusOK)
		return 3, nil // actual < estimate (10) → over-debit accepted
	}
	rr := doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	if rr.Code != nethttp.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if f.payee.DebitBalanceCalls != 1 {
		t.Errorf("DebitBalance calls: got %d, want 1 (no credit-back in v1)", f.payee.DebitBalanceCalls)
	}
}

func TestPaymentMiddleware_WorkIDStableAcrossIdenticalPayments(t *testing.T) {
	// Same payment blob → same work_id on both ProcessPayment calls.
	f := buildFixture(t)
	_ = doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	first := f.payee.LastProcessPaymentWorkID
	_ = doPaidRequest(t, f, `{"model":"test-model"}`, validHeader())
	second := f.payee.LastProcessPaymentWorkID
	if first == "" || first != second {
		t.Errorf("work_id should be stable for identical payment bytes; got %q then %q", first, second)
	}
}

func TestPaymentMiddleware_WorkIDDifferentForDifferentPayments(t *testing.T) {
	f := buildFixture(t)
	_ = doPaidRequest(t, f, `{"model":"test-model"}`, base64.StdEncoding.EncodeToString([]byte("payment-A")))
	first := f.payee.LastProcessPaymentWorkID
	_ = doPaidRequest(t, f, `{"model":"test-model"}`, base64.StdEncoding.EncodeToString([]byte("payment-B")))
	second := f.payee.LastProcessPaymentWorkID
	if first == "" || first == second {
		t.Errorf("work_id should differ for different payment bytes; got %q and %q", first, second)
	}
}

func TestMux_Register_DuplicateRoutePanics(t *testing.T) {
	f := buildFixture(t)
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register should panic")
		}
	}()
	f.mux.Register(nethttp.MethodGet, "/health", func(_ nethttp.ResponseWriter, _ *nethttp.Request) {})
	f.mux.Register(nethttp.MethodGet, "/health", func(_ nethttp.ResponseWriter, _ *nethttp.Request) {})
}

func TestMux_HasPaidCapability(t *testing.T) {
	f := buildFixture(t)
	if !f.mux.HasPaidCapability("openai:/v1/chat/completions") {
		t.Error("HasPaidCapability should report the registered capability")
	}
	if f.mux.HasPaidCapability("openai:/unregistered") {
		t.Error("HasPaidCapability should NOT report unregistered capability")
	}
}

func TestHealthHandler(t *testing.T) {
	f := buildFixture(t)
	RegisterUnpaidHandlers(f.mux, f.cfg)
	req := httptest.NewRequest(nethttp.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field: got %v, want ok", body["status"])
	}
	if fmt.Sprintf("%v", body["protocol_version"]) != fmt.Sprintf("%d", sharedyaml.CurrentProtocolVersion) {
		t.Errorf("protocol_version: got %v", body["protocol_version"])
	}
}

func TestCapabilitiesHandler(t *testing.T) {
	f := buildFixture(t)
	RegisterUnpaidHandlers(f.mux, f.cfg)
	req := httptest.NewRequest(nethttp.MethodGet, "/capabilities", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	raw, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(raw), `"capability":"openai:/v1/chat/completions"`) {
		t.Errorf("capabilities output missing capability: %s", raw)
	}
	if !strings.Contains(string(raw), `"model":"test-model"`) {
		t.Errorf("capabilities output missing model: %s", raw)
	}
	if strings.Contains(string(raw), "backend_url") {
		t.Errorf("capabilities output MUST NOT include backend_url: %s", raw)
	}
}

// assertErrorCode reads the JSON error envelope and asserts the code
// field. Shared helper so each test's failure message points at the
// contract violation cleanly.
func assertErrorCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var env struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("error body not JSON: %v (raw=%s)", err, body)
	}
	if env.Error != wantCode {
		t.Errorf("error code: got %q, want %q (detail=%q)", env.Error, wantCode, env.Detail)
	}
}
