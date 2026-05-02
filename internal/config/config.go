package config

import (
	"fmt"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// CurrentProtocolVersion is the shared worker.yaml schema version this
// worker build accepts.
const CurrentProtocolVersion = 1

// CurrentAPIVersion is the worker HTTP surface version advertised on
// /health. It evolves independently from CurrentProtocolVersion.
const CurrentAPIVersion = 1

// Config is the worker's projection of worker.yaml.
type Config struct {
	// ProtocolVersion is the shared worker.yaml schema version accepted
	// by this build. The parser validates the on-disk value equals
	// CurrentProtocolVersion before projection.
	ProtocolVersion int32
	// APIVersion is the worker HTTP contract version advertised on
	// /health.
	APIVersion int32
	// WorkerEthAddress is optional operator metadata surfaced only on
	// /registry/offerings when configured.
	WorkerEthAddress string
	// AuthToken is the optional bearer token protecting
	// /registry/offerings and /v1/payment/ticket-params.
	AuthToken string

	// Worker holds the worker-only fields.
	Worker WorkerSection

	// Capabilities exposes the parsed capability catalog in two views:
	//   - Ordered: iteration order matches the YAML, for deterministic
	//     route output and catalog comparison.
	//   - Route:   (capability, offering) -> routing target.
	Capabilities CapabilityCatalog
}

// WorkerSection holds the parsed worker-only fields.
type WorkerSection struct {
	HTTPListen                     string
	PaymentDaemonSocket            string
	MaxConcurrentRequests          int
	VerifyDaemonConsistencyOnStart bool
}

// CapabilityCatalog is the flattened routing table.
type CapabilityCatalog struct {
	Ordered []CapabilityEntry
	Route   map[RouteKey]OfferingRoute
}

// CapabilityEntry is one row of the ordered view.
type CapabilityEntry struct {
	Capability types.CapabilityID
	WorkUnit   types.WorkUnit
	Offerings  []OfferingEntry

	DebitCadenceSeconds        int
	SufficientMinRunwaySeconds int
	SufficientGraceSeconds     int
}

// OfferingEntry is one row of a capability's offering list.
type OfferingEntry struct {
	ID                  types.ModelID
	PricePerWorkUnitWei string
	BackendURL          string
}

// RouteKey is the composite lookup key.
type RouteKey struct {
	Capability types.CapabilityID
	Offering   types.ModelID
}

// OfferingRoute is the per-(capability, offering) routing target.
type OfferingRoute struct {
	Capability          types.CapabilityID
	Offering            types.ModelID
	WorkUnit            types.WorkUnit
	BackendURL          string
	PricePerWorkUnitWei string
}

// New constructs a *Config from its parts, building the flat Route map
// from the ordered capability list. Used by Load and by tests that
// build fixtures in memory.
func New(w WorkerSection, ordered []CapabilityEntry) *Config {
	cfg := &Config{
		ProtocolVersion: CurrentProtocolVersion,
		APIVersion:      CurrentAPIVersion,
		Worker:          w,
		Capabilities: CapabilityCatalog{
			Ordered: append([]CapabilityEntry(nil), ordered...),
			Route:   make(map[RouteKey]OfferingRoute, len(ordered)*2),
		},
	}
	for _, entry := range ordered {
		for _, o := range entry.Offerings {
			cfg.Capabilities.Route[RouteKey{Capability: entry.Capability, Offering: o.ID}] = OfferingRoute{
				Capability:          entry.Capability,
				Offering:            o.ID,
				WorkUnit:            entry.WorkUnit,
				BackendURL:          o.BackendURL,
				PricePerWorkUnitWei: o.PricePerWorkUnitWei,
			}
		}
	}
	return cfg
}

// Load reads, validates, and projects worker.yaml.
func Load(path string) (*Config, error) {
	parsed, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	if err := validate(parsed); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}
	return projectFromYAML(parsed), nil
}

// Lookup returns the routing target for a (capability, offering) pair,
// or false if unknown.
func (c *Config) Lookup(cap types.CapabilityID, offering types.ModelID) (OfferingRoute, bool) {
	r, ok := c.Capabilities.Route[RouteKey{Capability: cap, Offering: offering}]
	return r, ok
}
