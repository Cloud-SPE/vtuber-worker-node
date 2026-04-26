package config

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-payment-library/config/sharedyaml"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// Config is the worker-internal projection of sharedyaml.Config.
// Daemon-only fields (payment_daemon.*) are intentionally omitted —
// the worker ignores them. The capabilities block is flattened into a
// (CapabilityID, ModelID) → ModelRoute map for O(1) routing in the
// middleware.
type Config struct {
	// ProtocolVersion carried through from the YAML. Compared against
	// PayeeDaemon.ListCapabilities.protocol_version at startup.
	ProtocolVersion int32

	// Worker holds the worker-only fields (http_listen,
	// payment_daemon_socket, etc.).
	Worker WorkerSection

	// Capabilities exposes the parsed capability catalog in two views:
	//   - Ordered: iteration order matches the YAML (after
	//     sharedyaml.Validate normalizes), for deterministic
	//     /capabilities output and catalog comparison.
	//   - Route:   (capability, model) → routing target, for
	//     middleware and module dispatch.
	Capabilities CapabilityCatalog
}

// WorkerSection mirrors sharedyaml.WorkerConfig as Go fields the rest
// of the code uses directly.
type WorkerSection struct {
	HTTPListen                     string
	PaymentDaemonSocket            string
	MaxConcurrentRequests          int
	VerifyDaemonConsistencyOnStart bool

	// ServiceRegistryPublisher is nil when worker.yaml omits the
	// section. When non-nil, the worker runs the BuildSignWrite
	// startup flow against the publisher daemon at the configured
	// socket.
	ServiceRegistryPublisher *ServiceRegistryPublisherSection
}

// CapabilityCatalog is the flattened routing table.
type CapabilityCatalog struct {
	// Ordered is the full set as it appears in the YAML (post-
	// normalization). Iterate this for /capabilities output.
	Ordered []CapabilityEntry
	// Route is the flat lookup used on every request.
	Route map[RouteKey]ModelRoute
}

// ServiceRegistryPublisherSection is the worker-side projection of
// the optional sharedyaml ServiceRegistryPublisherConfig. Nil when
// the section is absent in worker.yaml — the worker skips publisher
// integration in that case.
type ServiceRegistryPublisherSection struct {
	PublisherDaemonSocket string
	ManifestOutPath       string
	OperatorEthAddress    string
	NodeID                string
	NodeURL               string
	AllowOnChainWrites    bool
	ServiceURI            string
}

// CapabilityEntry is one row of the ordered view.
type CapabilityEntry struct {
	Capability types.CapabilityID
	WorkUnit   types.WorkUnit
	Models     []ModelEntry

	// Streaming-only fields. Zero means "use the consumer module's
	// default" (typically 5/30/60 per ADR-006). Applied only by
	// streaming-session modules; one-shot Module capabilities ignore.
	DebitCadenceSeconds        int
	SufficientMinRunwaySeconds int
	SufficientGraceSeconds     int
}

// ModelEntry is one row of a capability's model list.
type ModelEntry struct {
	Model               types.ModelID
	PricePerWorkUnitWei string
	BackendURL          string
}

// RouteKey is the composite lookup key.
type RouteKey struct {
	Capability types.CapabilityID
	Model      types.ModelID
}

// ModelRoute is the per-(capability, model) routing target, materialized
// once at startup.
type ModelRoute struct {
	Capability          types.CapabilityID
	Model               types.ModelID
	WorkUnit            types.WorkUnit
	BackendURL          string
	PricePerWorkUnitWei string
}

// Load reads and validates a shared worker.yaml, then projects it to
// the worker-internal Config. Wraps sharedyaml.ParseFile +
// sharedyaml.Validate; returns a fatal error if either fails.
//
// Does NOT talk to the daemon. Catalog cross-check is a separate step
// (VerifyDaemonCatalog).
func Load(path string) (*Config, error) {
	shared, err := sharedyaml.ParseFile(path)
	if err != nil {
		return nil, err
	}
	if err := sharedyaml.Validate(shared); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}
	return FromShared(shared)
}

// FromShared projects a validated *sharedyaml.Config into a worker
// Config. Exposed for tests that construct their own config in memory.
// Returns an error only if the worker view fails an invariant the
// library's Validate doesn't cover (currently: none — this exists so
// tests can trust a non-error return means a complete projection).
func FromShared(shared *sharedyaml.Config) (*Config, error) {
	if shared == nil {
		return nil, fmt.Errorf("config: nil sharedyaml.Config")
	}
	cfg := &Config{
		ProtocolVersion: int32(shared.ProtocolVersion),
		Worker: WorkerSection{
			HTTPListen:                     shared.Worker.HTTPListen,
			PaymentDaemonSocket:            shared.Worker.PaymentDaemonSocket,
			MaxConcurrentRequests:          shared.Worker.MaxConcurrentRequests,
			VerifyDaemonConsistencyOnStart: shared.Worker.VerifyDaemonConsistencyOnStart,
			ServiceRegistryPublisher:       projectPublisher(shared.Worker.ServiceRegistryPublisher),
		},
		Capabilities: CapabilityCatalog{
			Ordered: make([]CapabilityEntry, 0, len(shared.Capabilities)),
			Route:   make(map[RouteKey]ModelRoute),
		},
	}
	for _, c := range shared.Capabilities {
		entry := CapabilityEntry{
			Capability:                 types.CapabilityID(c.Capability),
			WorkUnit:                   types.WorkUnit(c.WorkUnit),
			Models:                     make([]ModelEntry, 0, len(c.Models)),
			DebitCadenceSeconds:        c.DebitCadenceSeconds,
			SufficientMinRunwaySeconds: c.SufficientMinRunwaySeconds,
			SufficientGraceSeconds:     c.SufficientGraceSeconds,
		}
		for _, m := range c.Models {
			me := ModelEntry{
				Model:               types.ModelID(m.Model),
				PricePerWorkUnitWei: m.PricePerWorkUnitWei,
				BackendURL:          m.BackendURL,
			}
			entry.Models = append(entry.Models, me)
			cfg.Capabilities.Route[RouteKey{Capability: entry.Capability, Model: me.Model}] = ModelRoute{
				Capability:          entry.Capability,
				Model:               me.Model,
				WorkUnit:            entry.WorkUnit,
				BackendURL:          me.BackendURL,
				PricePerWorkUnitWei: me.PricePerWorkUnitWei,
			}
		}
		cfg.Capabilities.Ordered = append(cfg.Capabilities.Ordered, entry)
	}
	return cfg, nil
}

// Lookup returns the routing target for a (capability, model) pair, or
// false if unknown. Used by the middleware to resolve a request to a
// backend URL before it hits the module's Serve method.
func (c *Config) Lookup(cap types.CapabilityID, model types.ModelID) (ModelRoute, bool) {
	r, ok := c.Capabilities.Route[RouteKey{Capability: cap, Model: model}]
	return r, ok
}

// projectPublisher copies the optional sharedyaml ServiceRegistryPublisher
// section into the worker-internal type. Returns nil when the upstream
// section is absent — the worker runtime checks for nil to decide
// whether to dial the publisher daemon at startup.
func projectPublisher(p *sharedyaml.ServiceRegistryPublisherConfig) *ServiceRegistryPublisherSection {
	if p == nil {
		return nil
	}
	return &ServiceRegistryPublisherSection{
		PublisherDaemonSocket: p.PublisherDaemonSocket,
		ManifestOutPath:       p.ManifestOutPath,
		OperatorEthAddress:    p.OperatorEthAddress,
		NodeID:                p.NodeID,
		NodeURL:               p.NodeURL,
		AllowOnChainWrites:    p.AllowOnChainWrites,
		ServiceURI:            p.ServiceURI,
	}
}
