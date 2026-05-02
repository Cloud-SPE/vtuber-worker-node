package http

import (
	"encoding/json"
	"math/big"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTicketParamsHandler_HappyPath(t *testing.T) {
	f := buildFixture(t)
	f.payee.GetTicketParamsResponse = payeedaemon.TicketParams{
		Recipient:         mustHexBytes(t, "0xd00354656922168815fcd1e51cbddb9e359e3c7f"),
		FaceValueWei:      big.NewInt(1_250_000).Bytes(),
		WinProb:           []byte{0x12, 0x34, 0x56},
		RecipientRandHash: []byte{0xaa, 0xbb, 0xcc},
		Seed:              []byte{0xde, 0xad, 0xbe, 0xef},
		ExpirationBlock:   big.NewInt(9_876_543).Bytes(),
		ExpirationParams: payeedaemon.TicketExpirationParams{
			CreationRound:          4523,
			CreationRoundBlockHash: []byte{0x01, 0x02, 0x03, 0x04},
		},
	}
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodPost, "/v1/payment/ticket-params", strings.NewReader(`{
		"sender_eth_address":"0x1111111111111111111111111111111111111111",
		"recipient_eth_address":"0xd00354656922168815fcd1e51cbddb9e359e3c7f",
		"face_value_wei":"1250000",
		"capability":"openai:/v1/chat/completions",
		"offering":"test-model"
	}`))
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusOK {
		t.Fatalf("status: got %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if f.payee.GetTicketParamsCalls != 1 {
		t.Fatalf("GetTicketParamsCalls: got %d, want 1", f.payee.GetTicketParamsCalls)
	}
	if got := f.payee.LastGetTicketParams.FaceValue.String(); got != "1250000" {
		t.Fatalf("face value: got %s, want 1250000", got)
	}
	if got := f.payee.LastGetTicketParams.Capability; got != "openai:/v1/chat/completions" {
		t.Fatalf("capability: got %q", got)
	}
	if got := f.payee.LastGetTicketParams.Offering; got != "test-model" {
		t.Fatalf("offering: got %q", got)
	}

	var body struct {
		TicketParams struct {
			Recipient         string `json:"recipient"`
			FaceValue         string `json:"face_value"`
			WinProb           string `json:"win_prob"`
			RecipientRandHash string `json:"recipient_rand_hash"`
			Seed              string `json:"seed"`
			ExpirationBlock   string `json:"expiration_block"`
			ExpirationParams  struct {
				CreationRound          int64  `json:"creation_round"`
				CreationRoundBlockHash string `json:"creation_round_block_hash"`
			} `json:"expiration_params"`
		} `json:"ticket_params"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.TicketParams.Recipient != "0xd00354656922168815fcd1e51cbddb9e359e3c7f" {
		t.Fatalf("recipient: got %q", body.TicketParams.Recipient)
	}
	if body.TicketParams.FaceValue != "1250000" {
		t.Fatalf("face_value: got %q", body.TicketParams.FaceValue)
	}
	if body.TicketParams.WinProb != "0x123456" {
		t.Fatalf("win_prob: got %q", body.TicketParams.WinProb)
	}
	if body.TicketParams.RecipientRandHash != "0xaabbcc" {
		t.Fatalf("recipient_rand_hash: got %q", body.TicketParams.RecipientRandHash)
	}
	if body.TicketParams.Seed != "0xdeadbeef" {
		t.Fatalf("seed: got %q", body.TicketParams.Seed)
	}
	if body.TicketParams.ExpirationBlock != "9876543" {
		t.Fatalf("expiration_block: got %q", body.TicketParams.ExpirationBlock)
	}
	if body.TicketParams.ExpirationParams.CreationRound != 4523 {
		t.Fatalf("creation_round: got %d", body.TicketParams.ExpirationParams.CreationRound)
	}
	if body.TicketParams.ExpirationParams.CreationRoundBlockHash != "0x01020304" {
		t.Fatalf("creation_round_block_hash: got %q", body.TicketParams.ExpirationParams.CreationRoundBlockHash)
	}
}

func TestTicketParamsHandler_BearerAuth(t *testing.T) {
	f := buildFixture(t)
	f.cfg.AuthToken = "secret-token"
	f.payee.GetTicketParamsResponse = payeedaemon.TicketParams{}
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodPost, "/v1/payment/ticket-params", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusUnauthorized {
		t.Fatalf("unauthenticated status: got %d, want 401", rr.Code)
	}

	req = httptest.NewRequest(nethttp.MethodPost, "/v1/payment/ticket-params", strings.NewReader(`{
		"sender_eth_address":"0x1111111111111111111111111111111111111111",
		"recipient_eth_address":"0xd00354656922168815fcd1e51cbddb9e359e3c7f",
		"face_value_wei":"1250000",
		"capability":"openai:/v1/chat/completions",
		"offering":"test-model"
	}`))
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusOK {
		t.Fatalf("authenticated status: got %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
}

func TestTicketParamsHandler_BadRequest(t *testing.T) {
	f := buildFixture(t)
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodPost, "/v1/payment/ticket-params", strings.NewReader(`{
		"sender_eth_address":"0x1111111111111111111111111111111111111111",
		"recipient_eth_address":"0xd00354656922168815fcd1e51cbddb9e359e3c7f",
		"face_value_wei":"abc",
		"capability":"openai:/v1/chat/completions",
		"offering":"test-model"
	}`))
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "invalid_request")
	if f.payee.GetTicketParamsCalls != 0 {
		t.Fatalf("GetTicketParamsCalls: got %d, want 0", f.payee.GetTicketParamsCalls)
	}
}

func TestTicketParamsHandler_DaemonUnavailable(t *testing.T) {
	f := buildFixture(t)
	f.payee.GetTicketParamsError = status.Error(codes.Unavailable, "receiver daemon unavailable")
	RegisterUnpaidHandlers(f.mux, f.cfg)

	req := httptest.NewRequest(nethttp.MethodPost, "/v1/payment/ticket-params", strings.NewReader(`{
		"sender_eth_address":"0x1111111111111111111111111111111111111111",
		"recipient_eth_address":"0xd00354656922168815fcd1e51cbddb9e359e3c7f",
		"face_value_wei":"1250000",
		"capability":"openai:/v1/chat/completions",
		"offering":"test-model"
	}`))
	rr := httptest.NewRecorder()
	f.mux.Handler().ServeHTTP(rr, req)
	if rr.Code != nethttp.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", rr.Code)
	}
	assertErrorCode(t, rr.Body.Bytes(), "payment_daemon_unavailable")
}

func mustHexBytes(t *testing.T, s string) []byte {
	t.Helper()
	req, err := parseHexAddress("test", s)
	if err != nil {
		t.Fatalf("parseHexAddress(%q): %v", s, err)
	}
	return req
}
