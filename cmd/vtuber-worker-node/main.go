// Package main is the vtuber-worker-node binary entrypoint.
//
// Startup sequence:
//  1. Parse --config; Load + Validate the shared worker.yaml.
//  2. Dial the payee daemon's unix socket.
//  3. ListCapabilities; cross-check against the parsed config
//     (unless worker.verify_daemon_consistency_on_start = false).
//  4. Build providers (backend HTTP).
//  5. Build the Mux; register unpaid handlers; register every
//     capability module whose capability is declared in the config.
//  6. Start HTTP server; block until SIGINT / SIGTERM; graceful
//     shutdown with a 30s deadline.
//
// All startup failures exit non-zero. No partial-start fallbacks.
//
// M1 state: skeleton-only. The capability-module registry below is
// empty pending M3 of the vtuber-worker-node-bootstrap plan, which
// lands the `livepeer:vtuber-session` StreamingModule. A binary built
// at this commit will refuse to start against any non-empty config —
// that is the intended state for skeleton acceptance.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/backendhttp"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/publisherdaemon"
	rthttp "github.com/Cloud-SPE/vtuber-worker-node/internal/runtime/http"
	rtmetrics "github.com/Cloud-SPE/vtuber-worker-node/internal/runtime/metrics"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/service/modules/vtuber_session"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// version is stamped at build time via -ldflags for build_info.
// Defaults to "dev" so local builds still show up in the gauge.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run is the testable entrypoint. Returns a process exit code:
//
//	0  — clean shutdown (signal received)
//	1  — runtime failure (listen error, dial/catalog mismatch, etc.)
//	2  — flag / usage error
func run(args []string, stderr *os.File) int {
	fs := flag.NewFlagSet("vtuber-worker-node", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "/etc/livepeer/worker.yaml", "path to the shared worker.yaml")
	logLevel := fs.String("log-level", "info", "minimum log level: error|warn|info|debug")
	metricsListen := fs.String("metrics-listen", "", "host:port for the Prometheus /metrics HTTP listener; empty (default) disables it")
	metricsMaxSeriesPerMetric := fs.Int("metrics-max-series-per-metric", 10000, "max distinct label tuples per Prometheus metric vec; 0 disables the cap")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger := buildLogger(*logLevel, stderr)
	slog.SetDefault(logger)

	if err := validateMetricsListen(*metricsListen); err != nil {
		fmt.Fprintf(stderr, "invalid --metrics-listen %q: %v\n", *metricsListen, err)
		return 2
	}

	// 1. Load + validate config.
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", "path", *configPath, "err", err)
		return 1
	}
	logger.Info("config loaded",
		"path", *configPath,
		"protocol_version", cfg.ProtocolVersion,
		"capabilities", len(cfg.Capabilities.Ordered),
		"http_listen", cfg.Worker.HTTPListen,
		"metrics_listen", *metricsListen,
		"metrics_max_series_per_metric", *metricsMaxSeriesPerMetric)

	// 1b. Recorder. Prometheus when --metrics-listen is set, Noop
	// otherwise. Stamp build/health gauges once the recorder exists so
	// the values show up on the very first scrape.
	var recorder metrics.Recorder
	if *metricsListen != "" {
		recorder = metrics.NewPrometheus(metrics.PrometheusConfig{
			MaxSeriesPerMetric: *metricsMaxSeriesPerMetric,
			OnCapExceeded: func(name string, observed, capLimit int) {
				logger.Warn("metric cardinality cap exceeded; new label tuples dropped",
					"metric", name, "observed", observed, "cap", capLimit)
			},
		})
	} else {
		recorder = metrics.NewNoop()
	}
	recorder.SetBuildInfo(version, fmt.Sprintf("%d", cfg.ProtocolVersion), runtime.Version())
	recorder.SetMaxConcurrent(cfg.Worker.MaxConcurrentRequests)

	// 2. Dial the payee daemon.
	rawPayee, err := payeedaemon.Dial(cfg.Worker.PaymentDaemonSocket)
	if err != nil {
		logger.Error("dial payment daemon", "socket", cfg.Worker.PaymentDaemonSocket, "err", err)
		return 1
	}
	defer func() { _ = rawPayee.Close() }()
	payee := payeedaemon.WithMetrics(rawPayee, recorder)

	// 3. Cross-check catalog.
	if cfg.Worker.VerifyDaemonConsistencyOnStart {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		daemonCatalog, err := payee.ListCapabilities(ctx)
		if err != nil {
			logger.Error("ListCapabilities", "err", err)
			return 1
		}
		if err := config.VerifyDaemonCatalog(cfg, daemonCatalog); err != nil {
			logger.Error("daemon catalog mismatch — refusing to start", "err", err)
			return 1
		}
		logger.Info("daemon catalog verified", "capabilities", len(daemonCatalog.Capabilities))
	} else {
		logger.Warn("daemon consistency verification DISABLED by config — drift is possible",
			"hint", "flip worker.verify_daemon_consistency_on_start=true in worker.yaml for production")
	}

	// 4. Providers.
	// backend is the raw client; modules wrap it per-(capability,
	// model) inside Serve via their WithRecorder hook so the labels
	// match the request that's executing.
	backend := backendhttp.NewFetch()

	// 5. Mux + handlers + modules.
	mux := rthttp.NewMux(cfg, payee, logger).WithRecorder(recorder)
	rthttp.RegisterUnpaidHandlers(mux, cfg)

	registered := registerModules(mux, cfg, backend, recorder, logger)
	if registered == 0 {
		logger.Error("no capability modules registered — config has capabilities this build doesn't implement",
			"configured", cfg.Capabilities.Ordered,
			"hint", "M1 skeleton; the vtuber-session StreamingModule lands in M3 of vtuber-worker-node-bootstrap")
		return 1
	}
	if missing := missingCapabilityModules(mux, cfg); len(missing) > 0 {
		logger.Error("config declares capabilities with no module backing",
			"missing", missing)
		return 1
	}

	// 5b. Service-registry publisher integration (optional, gated by
	// the presence of `worker.service_registry_publisher` in worker.yaml).
	// First-run flow: build manifest, sign, atomically write to disk.
	// On-chain `setServiceURI` is gated behind allow_on_chain_writes.
	if pub := cfg.Worker.ServiceRegistryPublisher; pub != nil {
		if err := publishManifest(cfg, pub, logger); err != nil {
			logger.Error("service-registry publish failed", "err", err)
			return 1
		}
	} else {
		logger.Info("service-registry publisher not configured; skipping manifest publish",
			"hint", "set worker.service_registry_publisher.* in worker.yaml to enable")
	}

	// 6. Server.
	srv := rthttp.NewServer(mux, cfg.Worker.HTTPListen, logger)
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Start() }()

	// 6b. Optional metrics listener + uptime ticker. Both run for the
	// process lifetime; they share the same shutdown signal as the
	// main HTTP server.
	metricsListener, err := rtmetrics.NewListener(rtmetrics.Config{
		Addr:     *metricsListen,
		Recorder: recorder,
		Logger:   rtmetrics.SlogLogger{L: logger},
	})
	if err != nil {
		logger.Error("metrics listener: build", "err", err)
		return 1
	}
	metricsCtx, cancelMetrics := context.WithCancel(context.Background())
	defer cancelMetrics()
	metricsErr := make(chan error, 1)
	if metricsListener != nil {
		go func() { metricsErr <- metricsListener.Serve(metricsCtx) }()
	}
	uptimeStart := time.Now()
	uptimeTicker := time.NewTicker(30 * time.Second)
	defer uptimeTicker.Stop()
	uptimeDone := make(chan struct{})
	go func() {
		// Initial sample so /metrics has a non-zero uptime on the
		// first scrape (rather than waiting 30s for the first tick).
		recorder.SetUptimeSeconds(time.Since(uptimeStart).Seconds())
		for {
			select {
			case <-uptimeTicker.C:
				recorder.SetUptimeSeconds(time.Since(uptimeStart).Seconds())
			case <-uptimeDone:
				return
			}
		}
	}()
	defer close(uptimeDone)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil {
			logger.Error("server exited unexpectedly", "err", err)
			return 1
		}
		return 0
	case err := <-metricsErr:
		if err != nil {
			logger.Error("metrics listener exited unexpectedly", "err", err)
			return 1
		}
		return 0
	case s := <-sig:
		logger.Info("received signal; beginning graceful shutdown", "signal", s.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		return 1
	}
	cancelMetrics()
	if metricsListener != nil {
		<-metricsErr
	}
	// Drain the Start goroutine.
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("server error during shutdown", "err", err)
	}
	return 0
}

// validateMetricsListen rejects malformed --metrics-listen values
// before any I/O. Empty is valid (means "metrics off").
func validateMetricsListen(addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	_ = host // optional; empty host means "all interfaces"
	if port == "" {
		return errors.New("port required (e.g. :9093)")
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("port not an integer: %w", err)
	}
	if p < 1024 || p > 65535 {
		return fmt.Errorf("port %d outside [1024, 65535]", p)
	}
	return nil
}

// registerModules wires every capability module this binary supports.
// Only modules whose capability string appears in the config are
// registered — a build that supports a module the operator didn't
// configure stays quiet.
//
// Returns the number of modules actually registered. Add new modules
// by appending to the switch below; this is the one place where the
// binary's capability catalog is enumerated.
func registerModules(
	mux *rthttp.Mux,
	cfg *config.Config,
	backend backendhttp.Client,
	recorder metrics.Recorder,
	logger *slog.Logger,
) int {
	registered := 0
	for _, entry := range cfg.Capabilities.Ordered {
		switch entry.Capability {
		case vtuber_session.Capability:
			// Pick the first model's backend_url. Multi-model
			// dispatch (resolution × fps × GPU tiers) lands when
			// pricing-and-capability-registration ships; for the
			// initial bootstrap one model per capability is the
			// common case.
			backendURL := ""
			if len(entry.Models) > 0 {
				backendURL = entry.Models[0].BackendURL
			}
			modCfg := vtuber_session.Config{
				BackendURL: backendURL,
				Logger:     logger,
				// Bridge stays on the stub until vtuber-livepeer-bridge-mvp
				// lands the real WebSocket-backed implementation.
			}
			if entry.DebitCadenceSeconds > 0 {
				modCfg.DebitCadence = time.Duration(entry.DebitCadenceSeconds) * time.Second
			}
			if entry.SufficientMinRunwaySeconds > 0 {
				modCfg.RunwayMinUnits = uint64(entry.SufficientMinRunwaySeconds)
			}
			if entry.SufficientGraceSeconds > 0 {
				modCfg.GraceWindow = time.Duration(entry.SufficientGraceSeconds) * time.Second
			}
			mod := vtuber_session.New(modCfg)
			mux.RegisterStreamingRoute(mod)
			logger.Info("streaming capability registered",
				"capability", mod.Capability(),
				"path", mod.HTTPPath(),
				"models", len(entry.Models),
				"backend_url", backendURL,
				"debit_cadence_s", entry.DebitCadenceSeconds,
				"runway_min_s", entry.SufficientMinRunwaySeconds,
				"grace_s", entry.SufficientGraceSeconds)
			registered++
		default:
			logger.Warn("config declares capability this build doesn't implement",
				"capability", entry.Capability,
				"hint", "register a module under internal/service/modules/ and add a case here")
		}
	}
	return registered
}

// missingCapabilityModules reports any configured capability whose
// module wasn't registered. This catches the case where the switch in
// registerModules falls through for a capability the operator declared
// — we'd rather refuse to start than silently half-serve the catalog.
func missingCapabilityModules(mux *rthttp.Mux, cfg *config.Config) []types.CapabilityID {
	var missing []types.CapabilityID
	for _, entry := range cfg.Capabilities.Ordered {
		if !mux.HasPaidCapability(entry.Capability) {
			missing = append(missing, entry.Capability)
		}
	}
	return missing
}

// publishManifest runs the BuildSignWrite startup flow against the
// co-located service-registry-daemon publisher daemon. Called from
// main when worker.service_registry_publisher is non-nil. Errors are
// fatal — refusing to start when the manifest can't be published is
// safer than running with a stale on-disk file.
func publishManifest(
	cfg *config.Config,
	pub *config.ServiceRegistryPublisherSection,
	logger *slog.Logger,
) error {
	caps := make([]publisherdaemon.CapabilityInput, 0, len(cfg.Capabilities.Ordered))
	for _, e := range cfg.Capabilities.Ordered {
		models := make([]publisherdaemon.ModelInput, 0, len(e.Models))
		for _, m := range e.Models {
			models = append(models, publisherdaemon.ModelInput{
				ID:                  string(m.Model),
				PricePerWorkUnitWei: m.PricePerWorkUnitWei,
				Warm:                false,
				ConstraintsJSON:     nil,
			})
		}
		caps = append(caps, publisherdaemon.CapabilityInput{
			Name:     string(e.Capability),
			WorkUnit: string(e.WorkUnit),
			Models:   models,
		})
	}
	client, err := publisherdaemon.Dial(pub.PublisherDaemonSocket, logger)
	if err != nil {
		return fmt.Errorf("dial publisher daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	dialCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	txHash, err := client.BuildSignWrite(
		dialCtx,
		publisherdaemon.NodeIdentity{
			OperatorEthAddress: pub.OperatorEthAddress,
			NodeID:             pub.NodeID,
			NodeURL:            pub.NodeURL,
		},
		caps,
		pub.ManifestOutPath,
		pub.AllowOnChainWrites,
		pub.ServiceURI,
	)
	if err != nil {
		return err
	}
	logger.Info("service-registry publish complete",
		"manifest_out_path", pub.ManifestOutPath,
		"capabilities", len(caps),
		"on_chain_tx_hash", txHash)
	return nil
}

func buildLogger(level string, out *os.File) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "info":
		lv = slog.LevelInfo
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
		fmt.Fprintf(out, "unknown --log-level %q; defaulting to info\n", level)
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: lv}))
}
