package vtuber_session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPBackendClose_UsesSessionStopRoute(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	backend := NewHTTPBackend(srv.Client())
	if err := backend.Close(context.Background(), "gw_123", srv.URL+"/api/sessions/start"); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if gotPath != "/api/sessions/gw_123/stop" {
		t.Fatalf("stop path: got %q, want %q", gotPath, "/api/sessions/gw_123/stop")
	}
}

func TestHTTPBackendClose_TreatsNoActiveSessionAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":{"error":"no_active_session"}}`, http.StatusConflict)
	}))
	defer srv.Close()

	backend := NewHTTPBackend(srv.Client())
	if err := backend.Close(context.Background(), "gw_123", srv.URL+"/api/sessions/start"); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}
