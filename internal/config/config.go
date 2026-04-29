package config

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-modules-project/payment-daemon/config/sharedyaml"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// Config is the worker-internal projection of sharedyaml.Config.
// Daemon-only fields (payment_daemon.*) are intentionally omitted —
// the worker ignores them. The capabilities block is flattened into a
// (CapabilityID, ModelID) → OfferingRoute map for O(1) routing in the
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
}

// CapabilityCatalog is the flattened routing table.
type CapabilityCatalog struct {
	// Ordered is the full set as it appears in the YAML (post-
	// normalization). Iterate this for /capabilities output.
	Ordered []CapabilityEntry
	// Route is the flat lookup used on every request.
	Route map[RouteKey]OfferingRoute
}

// v3.0.0: ServiceRegistryPublisherSection removed. Workers do not
// self-publish under archetype A (suite plan 0003 §Decision 1).
// Whatever sharedyaml exposes for this section is ignored at projection
// time; if the operator has a leftover `service_registry_publisher`
// block in worker.yaml, the worker logs a warning and continues.

// CapabilityEntry is one row of the ordered view.
type CapabilityEntry struct {
	Capability types.CapabilityID
	WorkUnit   types.WorkUnit
	Offerings  []OfferingEntry

	// Streaming-only fields. Zero means "use the consumer module's
	// default" (typically 5/30/60 per ADR-006). Applied only by
	// streaming-session modules; one-shot Module capabilities ignore.
	DebitCadenceSeconds        int
	SufficientMinRunwaySeconds int
	SufficientGraceSeconds     int
}

// OfferingEntry is one row of a capability's model list.
type OfferingEntry struct {
	Model               types.ModelID
	PricePerWorkUnitWei string
	BackendURL          string
}

// RouteKey is the composite lookup key.
type RouteKey struct {
	Capability types.CapabilityID
	Model      types.ModelID
}

// OfferingRoute is the per-(capability, model) routing target, materialized
// once at startup.
type OfferingRoute struct {
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
		},
		Capabilities: CapabilityCatalog{
			Ordered: make([]CapabilityEntry, 0, len(shared.Capabilities)),
			Route:   make(map[RouteKey]OfferingRoute),
		},
	}
	for _, c := range shared.Capabilities {
		entry := CapabilityEntry{
			Capability:                 types.CapabilityID(c.Capability),
			WorkUnit:                   types.WorkUnit(c.WorkUnit),
			Offerings:                  make([]OfferingEntry, 0, len(c.Offerings)),
			DebitCadenceSeconds:        c.DebitCadenceSeconds,
			SufficientMinRunwaySeconds: c.SufficientMinRunwaySeconds,
			SufficientGraceSeconds:     c.SufficientGraceSeconds,
		}
		for _, m := range c.Offerings {
			me := OfferingEntry{
				Model:               types.ModelID(m.Model),
				PricePerWorkUnitWei: m.PricePerWorkUnitWei,
				BackendURL:          m.BackendURL,
			}
			entry.Offerings = append(entry.Offerings, me)
			cfg.Capabilities.Route[RouteKey{Capability: entry.Capability, Model: me.Model}] = OfferingRoute{
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
func (c *Config) Lookup(cap types.CapabilityID, model types.ModelID) (OfferingRoute, bool) {
	r, ok := c.Capabilities.Route[RouteKey{Capability: cap, Model: model}]
	return r, ok
}

