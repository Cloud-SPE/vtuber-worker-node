package config

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

type yamlConfig struct {
	ProtocolVersion          int              `yaml:"protocol_version"`
	WorkerEthAddress         string           `yaml:"worker_eth_address,omitempty"`
	AuthToken                string           `yaml:"auth_token,omitempty"`
	PaymentDaemon            rawYAMLNode      `yaml:"payment_daemon"`
	Worker                   yamlWorker       `yaml:"worker"`
	Capabilities             []yamlCapability `yaml:"capabilities"`
	ServiceRegistryPublisher rawYAMLNode      `yaml:"service_registry_publisher,omitempty"`
}

type yamlWorker struct {
	HTTPListen                     string `yaml:"http_listen"`
	PaymentDaemonSocket            string `yaml:"payment_daemon_socket"`
	MaxConcurrentRequests          int    `yaml:"max_concurrent_requests"`
	VerifyDaemonConsistencyOnStart bool   `yaml:"verify_daemon_consistency_on_start"`
}

type yamlCapability struct {
	Capability string         `yaml:"capability"`
	WorkUnit   string         `yaml:"work_unit"`
	Offerings  []yamlOffering `yaml:"offerings"`

	DebitCadenceSeconds        int `yaml:"debit_cadence_seconds,omitempty"`
	SufficientMinRunwaySeconds int `yaml:"sufficient_min_runway_seconds,omitempty"`
	SufficientGraceSeconds     int `yaml:"sufficient_grace_seconds,omitempty"`
}

type yamlOffering struct {
	ID                  string `yaml:"id"`
	PricePerWorkUnitWei string `yaml:"price_per_work_unit_wei"`
	BackendURL          string `yaml:"backend_url"`
}

type rawYAMLNode struct {
	Present bool
	Node    *yaml.Node
}

func (r *rawYAMLNode) UnmarshalYAML(value *yaml.Node) error {
	r.Present = true
	clone := *value
	r.Node = &clone
	return nil
}

var capabilityRE = regexp.MustCompile(`^[a-z][a-z0-9]*:.+$`)
var lowerEthAddressRE = regexp.MustCompile(`^0x[0-9a-f]{40}$`)

var knownWorkUnits = map[string]struct{}{
	"second":                {},
	"token":                 {},
	"character":             {},
	"audio_second":          {},
	"image_step_megapixel":  {},
	"video_frame_megapixel": {},
}

func parseFile(path string) (*yamlConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return parseReader(f)
}

func parseReader(r io.Reader) (*yamlConfig, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var cfg yamlConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}
	var tail yamlConfig
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("config: unexpected second YAML document; only one document per file is supported")
		}
		return nil, fmt.Errorf("config: trailing data after first document: %w", err)
	}
	return &cfg, nil
}

func validate(cfg *yamlConfig) error {
	if cfg == nil {
		return errors.New("config.validate: nil config")
	}
	if cfg.ServiceRegistryPublisher.Present {
		return errors.New("worker.yaml: 'service_registry_publisher' block is not supported in this worker; remove the block")
	}
	if cfg.ProtocolVersion != CurrentProtocolVersion {
		return fmt.Errorf("worker.yaml: protocol_version=%d is not supported by this worker build (CurrentProtocolVersion=%d); upgrade or downgrade one side", cfg.ProtocolVersion, CurrentProtocolVersion)
	}
	if !cfg.PaymentDaemon.Present {
		return errors.New("worker.yaml: missing 'payment_daemon' section (required for the receiver-mode daemon co-located with this worker)")
	}
	if cfg.WorkerEthAddress != "" && !lowerEthAddressRE.MatchString(strings.TrimSpace(cfg.WorkerEthAddress)) {
		return fmt.Errorf("worker_eth_address: must be a lowercased 0x-prefixed 40-hex address (got %q)", cfg.WorkerEthAddress)
	}
	if err := validateWorker(&cfg.Worker); err != nil {
		return err
	}
	return validateCapabilities(cfg.Capabilities)
}

func validateWorker(w *yamlWorker) error {
	if w.HTTPListen == "" {
		return errors.New("worker.http_listen: required")
	}
	if w.PaymentDaemonSocket == "" {
		return errors.New("worker.payment_daemon_socket: required")
	}
	if w.MaxConcurrentRequests <= 0 {
		return fmt.Errorf("worker.max_concurrent_requests: must be > 0 (got %d)", w.MaxConcurrentRequests)
	}
	return nil
}

func validateCapabilities(caps []yamlCapability) error {
	if len(caps) == 0 {
		return errors.New("capabilities: at least one capability required")
	}
	seen := make(map[string]struct{}, len(caps))
	for i, c := range caps {
		if err := validateCapability(i, &c); err != nil {
			return err
		}
		if _, dup := seen[c.Capability]; dup {
			return fmt.Errorf("capabilities[%d].capability: duplicate %q", i, c.Capability)
		}
		seen[c.Capability] = struct{}{}
	}
	return nil
}

func validateCapability(i int, c *yamlCapability) error {
	prefix := fmt.Sprintf("capabilities[%d]", i)
	if !capabilityRE.MatchString(c.Capability) {
		return fmt.Errorf(`%s.capability: must match ^[a-z][a-z0-9]*:.+$ (got %q)`, prefix, c.Capability)
	}
	if _, ok := knownWorkUnits[c.WorkUnit]; !ok {
		return fmt.Errorf("%s.work_unit: must be one of %s (got %q)", prefix, strings.Join(sortedKeys(knownWorkUnits), "|"), c.WorkUnit)
	}
	if len(c.Offerings) == 0 {
		return fmt.Errorf("%s.offerings: at least one offering required", prefix)
	}
	seen := make(map[string]struct{}, len(c.Offerings))
	for j, o := range c.Offerings {
		if err := validateOffering(prefix, j, &o); err != nil {
			return err
		}
		if _, dup := seen[o.ID]; dup {
			return fmt.Errorf("%s.offerings[%d].id: duplicate %q within capability", prefix, j, o.ID)
		}
		seen[o.ID] = struct{}{}
	}
	return nil
}

func validateOffering(capPrefix string, j int, o *yamlOffering) error {
	prefix := fmt.Sprintf("%s.offerings[%d]", capPrefix, j)
	if o.ID == "" {
		return fmt.Errorf("%s.id: required", prefix)
	}
	if o.PricePerWorkUnitWei == "" {
		return fmt.Errorf("%s.price_per_work_unit_wei: required", prefix)
	}
	price, ok := new(big.Int).SetString(o.PricePerWorkUnitWei, 10)
	if !ok {
		return fmt.Errorf("%s.price_per_work_unit_wei: %q is not a decimal integer", prefix, o.PricePerWorkUnitWei)
	}
	if price.Sign() <= 0 {
		return fmt.Errorf("%s.price_per_work_unit_wei: must be > 0 (got %q)", prefix, o.PricePerWorkUnitWei)
	}
	if o.BackendURL == "" {
		return fmt.Errorf("%s.backend_url: required", prefix)
	}
	if _, err := url.Parse(o.BackendURL); err != nil {
		return fmt.Errorf("%s.backend_url: %w", prefix, err)
	}
	return nil
}

func projectFromYAML(y *yamlConfig) *Config {
	ordered := make([]CapabilityEntry, 0, len(y.Capabilities))
	for _, c := range y.Capabilities {
		entry := CapabilityEntry{
			Capability:                 types.CapabilityID(c.Capability),
			WorkUnit:                   types.WorkUnit(c.WorkUnit),
			Offerings:                  make([]OfferingEntry, 0, len(c.Offerings)),
			DebitCadenceSeconds:        c.DebitCadenceSeconds,
			SufficientMinRunwaySeconds: c.SufficientMinRunwaySeconds,
			SufficientGraceSeconds:     c.SufficientGraceSeconds,
		}
		for _, o := range c.Offerings {
			entry.Offerings = append(entry.Offerings, OfferingEntry{
				ID:                  types.ModelID(o.ID),
				PricePerWorkUnitWei: o.PricePerWorkUnitWei,
				BackendURL:          o.BackendURL,
			})
		}
		ordered = append(ordered, entry)
	}
	cfg := New(WorkerSection{
		HTTPListen:                     y.Worker.HTTPListen,
		PaymentDaemonSocket:            y.Worker.PaymentDaemonSocket,
		MaxConcurrentRequests:          y.Worker.MaxConcurrentRequests,
		VerifyDaemonConsistencyOnStart: y.Worker.VerifyDaemonConsistencyOnStart,
	}, ordered)
	cfg.WorkerEthAddress = strings.TrimSpace(y.WorkerEthAddress)
	cfg.AuthToken = y.AuthToken
	return cfg
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
