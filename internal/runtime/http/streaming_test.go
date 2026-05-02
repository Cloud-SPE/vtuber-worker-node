package http

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
)

func TestStreamingTopupHandler_HappyPath(t *testing.T) {
	payee := payeedaemon.NewFake()
	registry := &streamingSessionRegistry{
		byGatewayID: map[string]streamingSessionInfo{
			"gw_123": {
				GatewaySessionID: "gw_123",
				WorkerSessionID:  "worker_abc",
				WorkID:           "work_456",
				Sender:           []byte{0x01, 0x02},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/gw_123/topup", nil)
	req.SetPathValue("gateway_session_id", "gw_123")
	req.Header.Set("X-Livepeer-Payment", base64.StdEncoding.EncodeToString([]byte("topup-payment")))
	rr := httptest.NewRecorder()

	streamingTopupHandler(payee, registry).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202 body=%s", rr.Code, rr.Body.String())
	}
	if payee.ProcessPaymentCalls != 1 {
		t.Fatalf("ProcessPaymentCalls: got %d, want 1", payee.ProcessPaymentCalls)
	}
	if payee.LastProcessPaymentWorkID != "work_456" {
		t.Fatalf("workID: got %q, want work_456", payee.LastProcessPaymentWorkID)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["worker_session_id"] != "worker_abc" {
		t.Fatalf("worker_session_id: got %q", body["worker_session_id"])
	}
}

func TestStreamingTopupHandler_UnknownSession(t *testing.T) {
	payee := payeedaemon.NewFake()
	registry := &streamingSessionRegistry{byGatewayID: map[string]streamingSessionInfo{}}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/gw_missing/topup", nil)
	req.SetPathValue("gateway_session_id", "gw_missing")
	req.Header.Set("X-Livepeer-Payment", base64.StdEncoding.EncodeToString([]byte("topup-payment")))
	rr := httptest.NewRecorder()

	streamingTopupHandler(payee, registry).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
	if payee.ProcessPaymentCalls != 0 {
		t.Fatalf("ProcessPaymentCalls: got %d, want 0", payee.ProcessPaymentCalls)
	}
}

func TestStreamingEndHandler_HappyPath(t *testing.T) {
	payee := payeedaemon.NewFake()
	registry := &streamingSessionRegistry{
		byGatewayID: map[string]streamingSessionInfo{
			"gw_123": {
				GatewaySessionID: "gw_123",
				WorkerSessionID:  "worker_abc",
				WorkID:           "work_456",
				Sender:           []byte{0x01, 0x02},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/gw_123/end", nil)
	req.SetPathValue("gateway_session_id", "gw_123")
	rr := httptest.NewRecorder()

	streamingEndHandler(payee, registry).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if payee.CloseSessionCalls != 1 {
		t.Fatalf("CloseSessionCalls: got %d, want 1", payee.CloseSessionCalls)
	}
	if payee.LastCloseSessionWorkID != "work_456" {
		t.Fatalf("workID: got %q, want work_456", payee.LastCloseSessionWorkID)
	}
	if _, ok := registry.Get("gw_123"); ok {
		t.Fatalf("registry entry still present after end")
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["status"] != "ended" {
		t.Fatalf("status body: got %q, want ended", body["status"])
	}
}

func TestStreamingEndHandler_UnknownSession(t *testing.T) {
	payee := payeedaemon.NewFake()
	registry := &streamingSessionRegistry{byGatewayID: map[string]streamingSessionInfo{}}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/gw_missing/end", nil)
	req.SetPathValue("gateway_session_id", "gw_missing")
	rr := httptest.NewRecorder()

	streamingEndHandler(payee, registry).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
	if payee.CloseSessionCalls != 0 {
		t.Fatalf("CloseSessionCalls: got %d, want 0", payee.CloseSessionCalls)
	}
}
