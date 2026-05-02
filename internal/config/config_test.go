package config

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

func goodConfig() *Config {
	return New(WorkerSection{
		HTTPListen:            "0.0.0.0:8080",
		PaymentDaemonSocket:   "/tmp/lpd.sock",
		MaxConcurrentRequests: 32,
	}, []CapabilityEntry{
		{
			Capability: "livepeer:vtuber-session",
			WorkUnit:   types.WorkUnitSecond,
			Offerings: []OfferingEntry{
				{ID: "vtuber-default-1080p30", PricePerWorkUnitWei: "6250", BackendURL: "http://localhost:8000"},
				{ID: "vtuber-premium-1440p60", PricePerWorkUnitWei: "18750", BackendURL: "http://localhost:8001"},
			},
		},
	})
}

func TestNew_FlatRouteMap(t *testing.T) {
	cfg := goodConfig()
	if got := len(cfg.Capabilities.Route); got != 2 {
		t.Errorf("route count: got %d, want 2 (one per offering)", got)
	}
	route, ok := cfg.Lookup("livepeer:vtuber-session", "vtuber-default-1080p30")
	if !ok {
		t.Fatal("Lookup(vtuber, default): not found")
	}
	if route.BackendURL != "http://localhost:8000" {
		t.Errorf("backend: got %q", route.BackendURL)
	}
	if route.WorkUnit != types.WorkUnitSecond {
		t.Errorf("work_unit: got %q, want second", route.WorkUnit)
	}
}

func TestLookup_UnknownOffering(t *testing.T) {
	cfg := goodConfig()
	if _, ok := cfg.Lookup("livepeer:vtuber-session", "unknown-offering"); ok {
		t.Error("expected Lookup miss")
	}
}

func TestLoad_RejectsUnsupportedProtocolVersion(t *testing.T) {
	if err := validate(&yamlConfig{
		ProtocolVersion: 99,
		PaymentDaemon:   rawYAMLNode{Present: true},
		Worker: yamlWorker{
			HTTPListen: "127.0.0.1:8080", PaymentDaemonSocket: "/tmp/pd.sock", MaxConcurrentRequests: 1,
		},
		Capabilities: []yamlCapability{{
			Capability: "livepeer:vtuber-session", WorkUnit: "second",
			Offerings: []yamlOffering{{ID: "vtuber-default-1080p30", PricePerWorkUnitWei: "6250", BackendURL: "http://localhost:8000"}},
		}},
	}); err == nil {
		t.Fatal("expected protocol version error")
	}
}

func TestVerifyDaemonCatalog_HappyPath(t *testing.T) {
	cfg := goodConfig()
	daemon := payeedaemon.ListCapabilitiesResult{
		Capabilities: []payeedaemon.Capability{
			{
				Capability: "livepeer:vtuber-session",
				WorkUnit:   "second",
				Offerings: []payeedaemon.OfferingPrice{
					{ID: "vtuber-default-1080p30", PricePerWorkUnitWei: "6250"},
					{ID: "vtuber-premium-1440p60", PricePerWorkUnitWei: "18750"},
				},
			},
		},
	}
	if err := VerifyDaemonCatalog(cfg, daemon); err != nil {
		t.Errorf("happy path: %v", err)
	}
}

func TestVerifyDaemonCatalog_PriceMismatch(t *testing.T) {
	cfg := goodConfig()
	daemon := payeedaemon.ListCapabilitiesResult{
		Capabilities: []payeedaemon.Capability{
			{
				Capability: "livepeer:vtuber-session",
				WorkUnit:   "second",
				Offerings: []payeedaemon.OfferingPrice{
					{ID: "vtuber-default-1080p30", PricePerWorkUnitWei: "1"},
					{ID: "vtuber-premium-1440p60", PricePerWorkUnitWei: "18750"},
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
	cfg := goodConfig()
	daemon := payeedaemon.ListCapabilitiesResult{}
	err := VerifyDaemonCatalog(cfg, daemon)
	if err == nil || !strings.Contains(err.Error(), "capability count mismatch") {
		t.Errorf("got %v, want count-mismatch error", err)
	}
}
