package config

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-modules-project/payment-daemon/config/sharedyaml"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

func goodShared() *sharedyaml.Config {
	return &sharedyaml.Config{
		ProtocolVersion: sharedyaml.CurrentProtocolVersion,
		Worker: sharedyaml.WorkerConfig{
			HTTPListen:            "0.0.0.0:8080",
			PaymentDaemonSocket:   "/tmp/lpd.sock",
			MaxConcurrentRequests: 32,
		},
		Capabilities: []sharedyaml.CapabilityConfig{
			{
				Capability: "openai:/v1/chat/completions",
				WorkUnit:   "token",
				Models: []sharedyaml.ModelConfig{
					{Model: "llama-3.3-70b", PricePerWorkUnitWei: "2000000000", BackendURL: "http://localhost:8000"},
					{Model: "mistral-7b-instruct", PricePerWorkUnitWei: "500000000", BackendURL: "http://localhost:8001"},
				},
			},
			{
				Capability: "openai:/v1/embeddings",
				WorkUnit:   "token",
				Models: []sharedyaml.ModelConfig{
					{Model: "text-embedding-3-small", PricePerWorkUnitWei: "100000000", BackendURL: "http://localhost:8002"},
				},
			},
		},
	}
}

func TestFromShared_FlatRouteMap(t *testing.T) {
	cfg, err := FromShared(goodShared())
	if err != nil {
		t.Fatalf("FromShared: %v", err)
	}
	if got := len(cfg.Capabilities.Route); got != 3 {
		t.Errorf("route count: got %d, want 3 (one per model across all capabilities)", got)
	}
	route, ok := cfg.Lookup("openai:/v1/chat/completions", "llama-3.3-70b")
	if !ok {
		t.Fatal("Lookup(chat, llama): not found")
	}
	if route.BackendURL != "http://localhost:8000" {
		t.Errorf("backend: got %q", route.BackendURL)
	}
	if route.WorkUnit != types.WorkUnitToken {
		t.Errorf("work_unit: got %q, want token", route.WorkUnit)
	}
}

func TestLookup_UnknownModel(t *testing.T) {
	cfg, err := FromShared(goodShared())
	if err != nil {
		t.Fatalf("FromShared: %v", err)
	}
	if _, ok := cfg.Lookup("openai:/v1/chat/completions", "unknown-model"); ok {
		t.Error("expected Lookup miss")
	}
}

func TestFromShared_NilConfig(t *testing.T) {
	if _, err := FromShared(nil); err == nil {
		t.Error("expected error on nil *sharedyaml.Config")
	}
}

func TestVerifyDaemonCatalog_HappyPath(t *testing.T) {
	cfg, _ := FromShared(goodShared())
	daemon := payeedaemon.ListCapabilitiesResult{
		ProtocolVersion: cfg.ProtocolVersion,
		Capabilities: []payeedaemon.Capability{
			{
				Capability: "openai:/v1/chat/completions",
				WorkUnit:   "token",
				Models: []payeedaemon.ModelPrice{
					{Model: "llama-3.3-70b", PricePerWorkUnitWei: "2000000000"},
					{Model: "mistral-7b-instruct", PricePerWorkUnitWei: "500000000"},
				},
			},
			{
				Capability: "openai:/v1/embeddings",
				WorkUnit:   "token",
				Models: []payeedaemon.ModelPrice{
					{Model: "text-embedding-3-small", PricePerWorkUnitWei: "100000000"},
				},
			},
		},
	}
	if err := VerifyDaemonCatalog(cfg, daemon); err != nil {
		t.Errorf("happy path: %v", err)
	}
}

func TestVerifyDaemonCatalog_ProtocolVersionMismatch(t *testing.T) {
	cfg, _ := FromShared(goodShared())
	daemon := payeedaemon.ListCapabilitiesResult{
		ProtocolVersion: cfg.ProtocolVersion + 1,
	}
	err := VerifyDaemonCatalog(cfg, daemon)
	if err == nil || !strings.Contains(err.Error(), "protocol_version") {
		t.Errorf("got %v, want error mentioning protocol_version", err)
	}
}

func TestVerifyDaemonCatalog_PriceMismatch(t *testing.T) {
	cfg, _ := FromShared(goodShared())
	daemon := payeedaemon.ListCapabilitiesResult{
		ProtocolVersion: cfg.ProtocolVersion,
		Capabilities: []payeedaemon.Capability{
			{
				Capability: "openai:/v1/chat/completions",
				WorkUnit:   "token",
				Models: []payeedaemon.ModelPrice{
					{Model: "llama-3.3-70b", PricePerWorkUnitWei: "1"},
					{Model: "mistral-7b-instruct", PricePerWorkUnitWei: "500000000"},
				},
			},
			{
				Capability: "openai:/v1/embeddings",
				WorkUnit:   "token",
				Models: []payeedaemon.ModelPrice{
					{Model: "text-embedding-3-small", PricePerWorkUnitWei: "100000000"},
				},
			},
		},
	}
	err := VerifyDaemonCatalog(cfg, daemon)
	if err == nil || !strings.Contains(err.Error(), "price mismatch") {
		t.Errorf("got %v, want price-mismatch error", err)
	}
}

func TestVerifyDaemonCatalog_CountMismatch(t *testing.T) {
	cfg, _ := FromShared(goodShared())
	daemon := payeedaemon.ListCapabilitiesResult{
		ProtocolVersion: cfg.ProtocolVersion,
		Capabilities:    nil,
	}
	err := VerifyDaemonCatalog(cfg, daemon)
	if err == nil || !strings.Contains(err.Error(), "capability count mismatch") {
		t.Errorf("got %v, want count-mismatch error", err)
	}
}
