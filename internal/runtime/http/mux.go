package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/service/modules"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// Mux is the worker's routing surface. Two entry points:
//
//   - Register(method, path, handler)     → unpaid; for /health,
//     /registry/offerings, /v1/payment/ticket-params.
//   - RegisterPaidRoute(module)           → wraps module in the
//     payment middleware and binds at (module.HTTPMethod,
//     module.HTTPPath).
//
// No direct access to the underlying http.ServeMux is exposed —
// forcing every paid request through paymentMiddleware is the only
// way core belief #3 stays enforceable.
//
// Paid-route concurrency is capped by a buffered-channel semaphore
// sized from cfg.Worker.MaxConcurrentRequests. Unpaid routes are NOT
// subject to the cap — health-check volume shouldn't starve inference.
type Mux struct {
	cfg      *config.Config
	payee    payeedaemon.Client
	logger   *slog.Logger
	recorder metrics.Recorder
	inner    *http.ServeMux

	// registered tracks every (method, path) already bound so we can
	// fail loudly on duplicates at startup rather than silently
	// shadowing.
	registered map[string]struct{}

	// paidCapabilities tracks capabilities that have a paid route
	// registered, so we can confirm all config-declared capabilities
	// have a module before Start.
	paidCapabilities map[types.CapabilityID]struct{}

	// paidSem is a non-blocking semaphore: cap = max_concurrent_requests,
	// len = currently-in-flight. Acquired on entry to paymentMiddleware;
	// failure returns 503 capacity_exhausted.
	paidSem chan struct{}

	streamingSessions *streamingSessionRegistry
}

// NewMux wires a Mux against a validated config and a connected
// payee-daemon client. The logger is threaded into paymentMiddleware
// for structured per-request event emission. The paid-route
// semaphore is sized from cfg.Worker.MaxConcurrentRequests; a non-
// positive value falls back to 1 (better than panicking at startup
// and less confusing than a zero-capacity channel that blocks
// everything).
func NewMux(cfg *config.Config, payee payeedaemon.Client, logger *slog.Logger) *Mux {
	if logger == nil {
		logger = slog.Default()
	}
	maxConcurrent := cfg.Worker.MaxConcurrentRequests
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Mux{
		cfg:              cfg,
		payee:            payee,
		logger:           logger,
		recorder:         metrics.NewNoop(),
		inner:            http.NewServeMux(),
		registered:       map[string]struct{}{},
		paidCapabilities: map[types.CapabilityID]struct{}{},
		paidSem:          make(chan struct{}, maxConcurrent),
		streamingSessions: &streamingSessionRegistry{
			byGatewayID: make(map[string]streamingSessionInfo),
		},
	}
}

// WithRecorder injects the metrics Recorder into every paid-route
// handler subsequently registered. Optional — defaults to noop. Call
// before RegisterPaidRoute so the recorder is closed over by the
// middleware.
func (m *Mux) WithRecorder(rec metrics.Recorder) *Mux {
	if rec == nil {
		rec = metrics.NewNoop()
	}
	m.recorder = rec
	return m
}

// InflightPaid returns the count of paid requests currently holding
// a semaphore slot. Exposed for /health.
func (m *Mux) InflightPaid() int {
	return len(m.paidSem)
}

// MaxConcurrentPaid returns the configured ceiling on paid-request
// concurrency. Exposed for /health.
func (m *Mux) MaxConcurrentPaid() int {
	return cap(m.paidSem)
}

// Register binds an unpaid handler. Panics on duplicate (method, path)
// — startup wiring mistakes should fail loudly.
func (m *Mux) Register(method, path string, h http.HandlerFunc) {
	key := method + " " + path
	if _, dup := m.registered[key]; dup {
		panic(fmt.Sprintf("Mux.Register: duplicate route %q", key))
	}
	m.registered[key] = struct{}{}
	m.inner.HandleFunc(key, h)
}

// RegisterPaidRoute wraps a Module in the payment middleware and binds
// its declared (HTTPMethod, HTTPPath). Panics on duplicate.
//
// This is the ONLY public API on Mux that mounts paid routes. Future
// custom lint (payment-middleware-check) verifies every capability
// module reaches the mux via this method.
func (m *Mux) RegisterPaidRoute(mod modules.Module) {
	key := mod.HTTPMethod() + " " + mod.HTTPPath()
	if _, dup := m.registered[key]; dup {
		panic(fmt.Sprintf("Mux.RegisterPaidRoute: duplicate route %q", key))
	}
	m.registered[key] = struct{}{}
	m.paidCapabilities[mod.Capability()] = struct{}{}

	handler := paymentMiddleware(paidRouteDeps{
		module:   mod,
		cfg:      m.cfg,
		payee:    m.payee,
		sem:      m.paidSem,
		logger:   m.logger,
		recorder: m.recorder,
	})
	m.inner.HandleFunc(key, handler)
}

// HasPaidCapability reports whether a module for the given capability
// has been registered. Useful for the startup check that every
// config-declared capability has a module backing it.
func (m *Mux) HasPaidCapability(c types.CapabilityID) bool {
	_, ok := m.paidCapabilities[c]
	return ok
}

// Handler exposes the underlying http.Handler for use by http.Server.
// Not for registering new routes — use Register / RegisterPaidRoute.
func (m *Mux) Handler() http.Handler {
	return m.inner
}

type streamingSessionRegistry struct {
	mu          sync.RWMutex
	byGatewayID map[string]streamingSessionInfo
}

type streamingSessionInfo struct {
	GatewaySessionID string
	WorkerSessionID  string
	WorkID           string
	BackendURL       string
	Sender           []byte
}

func (r *streamingSessionRegistry) Upsert(info streamingSessionInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
		r.byGatewayID[info.GatewaySessionID] = streamingSessionInfo{
			GatewaySessionID: info.GatewaySessionID,
			WorkerSessionID:  info.WorkerSessionID,
			WorkID:           info.WorkID,
			BackendURL:       info.BackendURL,
			Sender:           append([]byte(nil), info.Sender...),
		}
}

func (r *streamingSessionRegistry) Get(gatewaySessionID string) (streamingSessionInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.byGatewayID[gatewaySessionID]
	if !ok {
		return streamingSessionInfo{}, false
	}
	info.Sender = append([]byte(nil), info.Sender...)
	return info, true
}

func (r *streamingSessionRegistry) Delete(gatewaySessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byGatewayID, gatewaySessionID)
}
