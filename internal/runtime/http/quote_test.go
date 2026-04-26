package http

import (
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
)

func populateQuoteFake(f *payeedaemon.Fake) {
	f.GetQuoteResponse = payeedaemon.GetQuoteResult{
		TicketParams: payeedaemon.TicketParams{
			Recipient:         []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14},
			FaceValueWei:      []byte{0x01, 0x00, 0x00, 0x00}, // 0x01000000
			WinProb:           []byte{0xff, 0xff, 0xff, 0xff},
			RecipientRandHash: []byte{0xaa, 0xbb, 0xcc, 0xdd},
			Seed:              []byte{0xde, 0xad, 0xbe, 0xef},
			ExpirationBlock:   []byte{0x00, 0x12, 0x34, 0x56},
			ExpirationParams: payeedaemon.TicketExpirationParams{
				CreationRound:          42,
				CreationRoundBlockHash: []byte{0xca, 0xfe},
			},
		},
		ModelPrices: []payeedaemon.ModelPrice{
			{Model: "llama-3.3-70b", PricePerWorkUnitWei: "2000000000"},
			{Model: "mistral-7b-instruct", PricePerWorkUnitWei: "500000000"},
		},
	}
}

func TestQuoteHandler_HappyPath(t *testing.T) {
	f := buildFixture(t)
	populateQuoteFake(f.payee)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet,
		"/quote?sender=0x1234567890abcdef1234567890abcdef12345678&capability=openai:/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if f.payee.GetQuoteCalls != 1 {
		t.Errorf("GetQuote calls: got %d, want 1", f.payee.GetQuoteCalls)
	}
	if f.payee.LastGetQuoteCapability != "openai:/v1/chat/completions" {
		t.Errorf("capability: got %q", f.payee.LastGetQuoteCapability)
	}

	var out quoteJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rr.Body.String())
	}
	if out.TicketParams.Recipient != "0x0102030405060708090a0b0c0d0e0f1011121314" {
		t.Errorf("recipient hex: got %q", out.TicketParams.Recipient)
	}
	if out.TicketParams.ExpirationParams.CreationRound != 42 {
		t.Errorf("creation_round: got %d", out.TicketParams.ExpirationParams.CreationRound)
	}
	if len(out.ModelPrices) != 2 || out.ModelPrices[0].Model != "llama-3.3-70b" {
		t.Errorf("model_prices: unexpected %+v", out.ModelPrices)
	}
}

func TestQuoteHandler_MissingSender(t *testing.T) {
	f := buildFixture(t)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet, "/quote?capability=openai:/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != nethttp.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "invalid_request")
	if f.payee.GetQuoteCalls != 0 {
		t.Errorf("GetQuote must not be called on bad input")
	}
}

func TestQuoteHandler_BadSender(t *testing.T) {
	f := buildFixture(t)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet,
		"/quote?sender=not-hex&capability=openai:/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != nethttp.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "invalid_request")
}

func TestQuoteHandler_MissingCapability(t *testing.T) {
	f := buildFixture(t)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet,
		"/quote?sender=0x1234567890abcdef1234567890abcdef12345678", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != nethttp.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
}

func TestQuoteHandler_DaemonError(t *testing.T) {
	f := buildFixture(t)
	f.payee.GetQuoteError = errors.New("capability not configured")
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet,
		"/quote?sender=0x1234567890abcdef1234567890abcdef12345678&capability=openai:/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != nethttp.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "backend_unavailable")
}

func TestQuotesHandler_HappyPath(t *testing.T) {
	f := buildFixture(t)
	populateQuoteFake(f.payee)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet,
		"/quotes?sender=0x1234567890abcdef1234567890abcdef12345678", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	// buildFixture's cfg has one capability; /quotes calls GetQuote once.
	if f.payee.GetQuoteCalls != 1 {
		t.Errorf("GetQuote calls: got %d, want 1", f.payee.GetQuoteCalls)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"capability":"openai:/v1/chat/completions"`) {
		t.Errorf("quotes body missing capability: %s", body)
	}
	if !strings.Contains(body, `"model":"llama-3.3-70b"`) {
		t.Errorf("quotes body missing model: %s", body)
	}
}

func TestQuotesHandler_FailClosedOnAnyError(t *testing.T) {
	// When one capability errors, the whole /quotes response fails.
	f := buildFixture(t)
	f.payee.GetQuoteError = errors.New("daemon unreachable")
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet,
		"/quotes?sender=0x1234567890abcdef1234567890abcdef12345678", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != nethttp.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
}

func TestQuotesHandler_MissingSender(t *testing.T) {
	f := buildFixture(t)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodGet, "/quotes", nil)
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)

	if rr.Code != nethttp.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
}
