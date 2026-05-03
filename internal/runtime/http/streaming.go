package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/metrics"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/service/modules"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// RegisterStreamingRoute wraps a StreamingModule in the streaming-
// payment middleware and binds its declared (HTTPMethod, HTTPPath).
// Panics on duplicate route. Companion to RegisterPaidRoute for the
// long-lived streaming-session class of capabilities.
//
// The middleware: validates the X-Livepeer-Payment header, calls
// ProcessPayment to credit the session's balance, constructs a
// PaymentSession adapter, writes a 202 Accepted to the bridge, then
// invokes module.Serve and blocks for the session lifetime. The module
// is responsible for periodic Debit ticks, Sufficient checks, low-
// balance signalling on the WebSocket, and PaymentSession.Close.
func (m *Mux) RegisterStreamingRoute(mod modules.StreamingModule) {
	key := mod.HTTPMethod() + " " + mod.HTTPPath()
	if _, dup := m.registered[key]; dup {
		panic(fmt.Sprintf("Mux.RegisterStreamingRoute: duplicate route %q", key))
	}
	m.registered[key] = struct{}{}
	m.paidCapabilities[mod.Capability()] = struct{}{}

	handler := streamingMiddleware(streamingDeps{
		module:   mod,
		cfg:      m.cfg,
		payee:    m.payee,
		sem:      m.paidSem,
		logger:   m.logger,
		recorder: m.recorder,
		sessions: m.streamingSessions,
	})
	m.inner.HandleFunc(key, handler)

	topupKey := http.MethodPost + " /api/sessions/{gateway_session_id}/topup"
	if _, dup := m.registered[topupKey]; !dup {
		m.registered[topupKey] = struct{}{}
		m.inner.HandleFunc(topupKey, streamingTopupHandler(m.payee, m.streamingSessions))
	}

	endKey := http.MethodPost + " /api/sessions/{gateway_session_id}/end"
	if _, dup := m.registered[endKey]; !dup {
		m.registered[endKey] = struct{}{}
		var terminator modules.SessionTerminator
		if t, ok := mod.(modules.SessionTerminator); ok {
			terminator = t
		}
		m.inner.HandleFunc(endKey, streamingEndHandler(m.payee, m.streamingSessions, terminator))
	}
}

// streamingDeps bundles the per-route state captured by the
// streaming middleware closure.
type streamingDeps struct {
	module   modules.StreamingModule
	cfg      *config.Config
	payee    payeedaemon.Client
	sem      chan struct{}
	logger   *slog.Logger
	recorder metrics.Recorder
	sessions *streamingSessionRegistry
}

// streamingMiddleware is the streaming-session counterpart to the
// request/response paymentMiddleware. Differences from the request/
// response middleware:
//
//   - No upfront Debit before invoking the module. The module owns
//     the debit cadence via PaymentSession.Debit.
//   - Writes 202 Accepted immediately after ProcessPayment succeeds,
//     so the bridge unblocks and the customer-side WebSocket can
//     register session-id with the bridge. Subsequent state flows
//     over the bridge WebSocket, not over this HTTP response.
//   - Does NOT call PaymentSession.Close; the module is the sole
//     owner of that call (defer'd in module.Serve so panic paths
//     also Close).
func streamingMiddleware(d streamingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Concurrency cap (shared with one-shot paid routes). The
		// semaphore is released by the goroutine that runs the session
		// — see step 7 — so it spans the session lifetime, not just
		// the HTTP request.
		select {
		case d.sem <- struct{}{}:
		default:
			http.Error(w, "capacity_exhausted", http.StatusServiceUnavailable)
			return
		}
		releaseSem := func() { <-d.sem }
		semReleased := false
		defer func() {
			if !semReleased {
				releaseSem()
			}
		}()

		// 2. Read the body once; we forward it to the backend in
		// module.Serve via the request, so we have to buffer + reset
		// the body reader (no other path Re-reads it; this is
		// idempotent if module.Serve drains).
		body, err := io.ReadAll(io.LimitReader(r.Body, streamingMaxBodyBytes))
		if err != nil {
			http.Error(w, "body_read: "+err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))

		// 3. Validate payment header.
		paymentB64 := r.Header.Get("X-Livepeer-Payment")
		if paymentB64 == "" {
			http.Error(w, "missing X-Livepeer-Payment", http.StatusPaymentRequired)
			return
		}
		paymentBytes, err := base64.StdEncoding.DecodeString(paymentB64)
		if err != nil {
			http.Error(w, "X-Livepeer-Payment not base64: "+err.Error(), http.StatusBadRequest)
			return
		}

		gatewaySessionID, err := extractGatewaySessionID(r, body)
		if err != nil {
			http.Error(w, "invalid session identity: "+err.Error(), http.StatusBadRequest)
			return
		}
		r.Header.Set("X-Vtuber-Session-Id", gatewaySessionID)

			offering, err := extractStreamingOffering(r, body)
			if err != nil {
				http.Error(w, "invalid offering: "+err.Error(), http.StatusBadRequest)
				return
			}
			route, ok := d.cfg.Lookup(d.module.Capability(), types.ModelID(offering))
			if !ok {
				http.Error(w, "unknown offering: "+offering, http.StatusNotFound)
				return
			}
			r.Header.Set("X-Vtuber-Offering", offering)
			r.Header.Set("X-Vtuber-Backend-Url", route.BackendURL)

			// 4. OpenSession + ProcessPayment.
			workID := string(deriveWorkID(paymentBytes))
			workerSessionID := deriveWorkerSessionID(workID)
			r.Header.Set("X-Vtuber-Worker-Session-Id", workerSessionID)
			r.Header.Set("X-Vtuber-Work-Id", workID)
			pricePerUnit, ok := new(big.Int).SetString(route.PricePerWorkUnitWei, 10)
			if !ok {
				http.Error(w, "invalid route price_per_work_unit_wei", http.StatusInternalServerError)
				return
			}
			if _, err := d.payee.OpenSession(r.Context(), payeedaemon.OpenSessionRequest{
				WorkID:              workID,
				Capability:          string(route.Capability),
				Offering:            string(route.Offering),
				PricePerWorkUnitWei: pricePerUnit,
				WorkUnit:            string(route.WorkUnit),
			}); err != nil {
				d.logger.Warn("streaming: OpenSession failed",
					"err", err, "capability", d.module.Capability(), "offering", offering)
				http.Error(w, "OpenSession: "+err.Error(), http.StatusBadRequest)
				return
			}
			ppRes, err := d.payee.ProcessPayment(r.Context(), paymentBytes, workID)
		if err != nil {
			d.logger.Warn("streaming: ProcessPayment failed",
				"err", err, "capability", d.module.Capability())
			http.Error(w, "ProcessPayment: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 5. Construct the PaymentSession adapter for this session.
			d.sessions.Upsert(streamingSessionInfo{
				GatewaySessionID: gatewaySessionID,
				WorkerSessionID:  workerSessionID,
				WorkID:           workID,
				BackendURL:       route.BackendURL,
				Sender:           ppRes.Sender,
			})
		ps := newPaymentSessionAdapter(d.payee, ppRes.Sender, workID, d.recorder, func() {
			d.sessions.Delete(gatewaySessionID)
		})

		// 6. 202 Accepted to the bridge. The body echoes the work_id
		// so the bridge can correlate this session against its own
		// session-id space if needed. We set Content-Length so the
		// bridge's fetch resolves on the body bytes alone, without
		// waiting for the connection to close (which only happens at
		// session end).
		respBody, err := json.Marshal(map[string]string{
			"status":             "starting",
			"gateway_session_id": gatewaySessionID,
			"worker_session_id":  workerSessionID,
			"work_id":            workID,
		})
		if err != nil {
			http.Error(w, "internal: encode response: "+err.Error(), http.StatusInternalServerError)
			return
		}
		respBody = append(respBody, '\n')
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(respBody)))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(respBody)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// 7. Detach the session lifetime from the HTTP request. Run
		// module.Serve in a goroutine so the HTTP response closes
		// immediately after the 202 is on the wire, unblocking the
		// bridge's session-create handler. Subsequent state flows
		// over the bridge WebSocket the module opens (control_url
		// in the request body); not over this HTTP response.
		//
		// Use context.WithoutCancel so the goroutine survives the
		// HTTP request's context cancellation. The session's natural
		// terminations (graceful close, fatal error, balance exhausted)
		// drive shutdown via module.Serve's own paths.
		sessionCtx := context.WithoutCancel(r.Context())
		semReleased = true // ownership transfers to the goroutine
		go func() {
			defer releaseSem()
			if err := d.module.Serve(sessionCtx, r, ps); err != nil {
				d.logger.Warn("streaming: Serve returned error",
					"capability", d.module.Capability(),
					"work_id", workID,
					"err", err)
			}
		}()
	}
}

// streamingMaxBodyBytes caps the session-open request body. Streaming
// session-open bodies are JSON metadata only (persona + avatar URL +
// voice + render config + egress block + bearers); 256 KiB is generous.
// Media bytes do NOT flow through this endpoint — they go via the
// session-runner's own chunked-POST path to Pipeline egress per
// ADR-007.
const streamingMaxBodyBytes = 256 * 1024

// paymentSessionAdapter wraps a payeedaemon.Client into the
// modules.PaymentSession surface. The wrapper closes over (sender,
// work_id) so the module's call sites stay clean — they don't need
// to thread credentials through every Debit / Sufficient / Close.
type paymentSessionAdapter struct {
	payee   payeedaemon.Client
	sender  []byte
	workID  string
	rec     metrics.Recorder
	closed  bool
	onClose func()
}

func newPaymentSessionAdapter(c payeedaemon.Client, sender []byte, workID string, rec metrics.Recorder, onClose func()) *paymentSessionAdapter {
	return &paymentSessionAdapter{
		payee:   c,
		sender:  append([]byte(nil), sender...),
		workID:  workID,
		rec:     rec,
		onClose: onClose,
	}
}

func (p *paymentSessionAdapter) Debit(ctx context.Context, units uint64, debitSeq uint64) (int64, error) {
	res, err := p.payee.DebitBalance(ctx, p.sender, p.workID, int64(units), debitSeq)
	if err != nil {
		return 0, err
	}
	if res.BalanceWei == nil {
		return 0, nil
	}
	return wei64(res.BalanceWei), nil
}

func (p *paymentSessionAdapter) Sufficient(ctx context.Context, minUnits uint64) (bool, error) {
	res, err := p.payee.SufficientBalance(ctx, p.sender, p.workID, int64(minUnits))
	if err != nil {
		return false, err
	}
	return res.Sufficient, nil
}

func (p *paymentSessionAdapter) Close(ctx context.Context) error {
	if p.closed {
		return nil
	}
	p.closed = true
	if p.onClose != nil {
		defer p.onClose()
	}
	return p.payee.CloseSession(ctx, p.sender, p.workID)
}

func streamingTopupHandler(payee payeedaemon.Client, sessions *streamingSessionRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gatewaySessionID := strings.TrimSpace(r.PathValue("gateway_session_id"))
		if gatewaySessionID == "" {
			http.Error(w, "gateway_session_id required", http.StatusBadRequest)
			return
		}
		info, ok := sessions.Get(gatewaySessionID)
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		paymentB64 := r.Header.Get("X-Livepeer-Payment")
		if paymentB64 == "" {
			http.Error(w, "missing X-Livepeer-Payment", http.StatusPaymentRequired)
			return
		}
		paymentBytes, err := base64.StdEncoding.DecodeString(paymentB64)
		if err != nil {
			http.Error(w, "X-Livepeer-Payment not base64: "+err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := payee.ProcessPayment(r.Context(), paymentBytes, info.WorkID); err != nil {
			http.Error(w, "ProcessPayment: "+err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":             "credited",
			"gateway_session_id": gatewaySessionID,
			"worker_session_id":  info.WorkerSessionID,
			"work_id":            info.WorkID,
		})
	}
}

func streamingEndHandler(
	payee payeedaemon.Client,
	sessions *streamingSessionRegistry,
	terminator modules.SessionTerminator,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gatewaySessionID := strings.TrimSpace(r.PathValue("gateway_session_id"))
		if gatewaySessionID == "" {
			http.Error(w, "gateway_session_id required", http.StatusBadRequest)
			return
		}
		info, ok := sessions.Get(gatewaySessionID)
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		if terminator != nil {
			closeCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				err := terminator.TerminateSession(closeCtx, gatewaySessionID, info.BackendURL)
			cancel()
			if err != nil {
				http.Error(w, "backend stop: "+err.Error(), http.StatusBadGateway)
				return
			}
		}
		if err := payee.CloseSession(r.Context(), info.Sender, info.WorkID); err != nil {
			http.Error(w, "CloseSession: "+err.Error(), http.StatusBadRequest)
			return
		}
		sessions.Delete(gatewaySessionID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":             "ended",
			"gateway_session_id": gatewaySessionID,
			"worker_session_id":  info.WorkerSessionID,
			"work_id":            info.WorkID,
		})
	}
}

func extractGatewaySessionID(r *http.Request, body []byte) (string, error) {
	if r == nil {
		return "", errors.New("nil request")
	}
	if id := strings.TrimSpace(r.Header.Get("X-Vtuber-Session-Id")); id != "" {
		return id, nil
	}
	var payload struct {
		GatewaySessionID string `json:"gateway_session_id"`
		SessionID        string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if id := strings.TrimSpace(payload.GatewaySessionID); id != "" {
		return id, nil
	}
	if id := strings.TrimSpace(payload.SessionID); id != "" {
		return id, nil
	}
	return "", errors.New("missing gateway_session_id or session_id")
}

func extractStreamingOffering(r *http.Request, body []byte) (string, error) {
	if r == nil {
		return "", errors.New("nil request")
	}
	if offering := strings.TrimSpace(r.Header.Get("X-Vtuber-Offering")); offering != "" {
		return offering, nil
	}
	var payload struct {
		Offering string `json:"offering"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", errors.New("missing X-Vtuber-Offering header")
	}
	if offering := strings.TrimSpace(payload.Offering); offering != "" {
		return offering, nil
	}
	return "", errors.New("missing X-Vtuber-Offering header")
}

func deriveWorkerSessionID(workID string) string {
	if workID == "" {
		return "worker_session_unknown"
	}
	return "worker_" + workID
}

// wei64 clamps a *big.Int wei value into an int64 for the
// PaymentSession.Debit return shape. The streaming-session module
// uses balance only for negative-overdebit detection; full wei
// precision isn't needed at the module level (the daemon retains it).
func wei64(b *big.Int) int64 {
	if b == nil {
		return 0
	}
	if !b.IsInt64() {
		// Saturate to avoid overflow. Negative overflow (which
		// shouldn't happen in practice) would be a fatal balance.
		if b.Sign() < 0 {
			return -1
		}
		return 1<<62 - 1
	}
	return b.Int64()
}
