package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"
)

var ethAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// parseSender pulls the sender query param (0x-prefixed 40-hex ETH
// address) and returns it decoded. Rejects missing or malformed input.
func parseSender(r *http.Request) ([]byte, error) {
	s := strings.TrimSpace(r.URL.Query().Get("sender"))
	if s == "" {
		return nil, errors.New("sender query param required")
	}
	if !ethAddressRE.MatchString(s) {
		return nil, fmt.Errorf("sender %q: not a valid 0x-prefixed 40-hex address", s)
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("sender decode: %w", err)
	}
	return b, nil
}

// parseQuoteParams pulls sender + capability for /quote.
func parseQuoteParams(r *http.Request) (sender []byte, capability string, err error) {
	sender, err = parseSender(r)
	if err != nil {
		return nil, "", err
	}
	capability = strings.TrimSpace(r.URL.Query().Get("capability"))
	if capability == "" {
		return nil, "", errors.New("capability query param required")
	}
	return sender, capability, nil
}

// RegisterUnpaidHandlers binds the standard unpaid routes on mux.
// Called from server wiring; split out so tests can bind a mux
// without the full Server.
func RegisterUnpaidHandlers(m *Mux, cfg *config.Config) {
	m.Register(http.MethodGet, "/health", healthHandler(cfg, m))
	m.Register(http.MethodGet, "/capabilities", capabilitiesHandler(cfg))
	m.Register(http.MethodGet, "/quote", quoteHandler(cfg, m.payee))
	m.Register(http.MethodGet, "/quotes", quotesHandler(cfg, m.payee))
}

// healthHandler reports liveness + protocol version + configured
// capacity + current paid-route inflight count. The inflight value
// reflects live semaphore usage — useful both for operator dashboards
// and for bridge-side load-awareness if we ever add a "prefer-less-
// loaded-worker" feature.
func healthHandler(cfg *config.Config, mux *Mux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":           "ok",
			"protocol_version": cfg.ProtocolVersion,
			"max_concurrent":   mux.MaxConcurrentPaid(),
			"inflight":         mux.InflightPaid(),
		})
	}
}

// capabilitiesHandler emits the worker's capability catalog in the
// ordered form the config carries. backend_url is deliberately
// omitted — the bridge shouldn't see where inference is hosted.
func capabilitiesHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		out := struct {
			ProtocolVersion int32            `json:"protocol_version"`
			Capabilities    []capabilityJSON `json:"capabilities"`
		}{
			ProtocolVersion: cfg.ProtocolVersion,
			Capabilities:    make([]capabilityJSON, 0, len(cfg.Capabilities.Ordered)),
		}
		for _, c := range cfg.Capabilities.Ordered {
			models := make([]modelJSON, 0, len(c.Models))
			for _, m := range c.Models {
				models = append(models, modelJSON{
					Model:               string(m.Model),
					PricePerWorkUnitWei: m.PricePerWorkUnitWei,
				})
			}
			out.Capabilities = append(out.Capabilities, capabilityJSON{
				Capability: string(c.Capability),
				WorkUnit:   string(c.WorkUnit),
				Models:     models,
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

type capabilityJSON struct {
	Capability string      `json:"capability"`
	WorkUnit   string      `json:"work_unit"`
	Models     []modelJSON `json:"models"`
}

type modelJSON struct {
	Model               string `json:"model"`
	PricePerWorkUnitWei string `json:"price_per_work_unit_wei"`
}

// quoteHandler proxies GET /quote?sender=0x…&capability=<str> through
// to PayeeDaemon.GetQuote. Returns the TicketParams + per-model
// pricing as JSON. Hex-encodes every byte field (recipient,
// face_value, win_prob, recipient_rand_hash, seed, expiration_block,
// creation_round_block_hash) so the bridge can unmarshal via its
// existing z.string() schemas without binary handling.
func quoteHandler(cfg *config.Config, payee payeedaemon.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sender, capability, err := parseQuoteParams(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		out, status, err := fetchQuoteJSON(r.Context(), payee, sender, capability)
		if err != nil {
			writeJSONError(w, status, "backend_unavailable", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// quotesHandler proxies GET /quotes?sender=0x… — batched form that
// calls PayeeDaemon.GetQuote for every capability in the worker's
// catalog. Each per-capability call is synchronous; the catalog
// contains ~10 entries in practice so sequential is fine. If one
// capability errors, the whole response fails — partial results
// would let the bridge confidently cache a stale view.
func quotesHandler(cfg *config.Config, payee payeedaemon.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sender, err := parseSender(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		type entry struct {
			Capability string     `json:"capability"`
			Quote      *quoteJSON `json:"quote"`
		}
		out := struct {
			Quotes []entry `json:"quotes"`
		}{Quotes: make([]entry, 0, len(cfg.Capabilities.Ordered))}
		for _, c := range cfg.Capabilities.Ordered {
			q, status, qerr := fetchQuoteJSON(r.Context(), payee, sender, string(c.Capability))
			if qerr != nil {
				writeJSONError(w, status, "backend_unavailable", fmt.Sprintf("capability=%q: %v", c.Capability, qerr))
				return
			}
			out.Quotes = append(out.Quotes, entry{Capability: string(c.Capability), Quote: q})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// quoteJSON is the wire shape returned by /quote and nested by
// /quotes. Matches what the bridge's NodeQuoteResponseSchema expects.
type quoteJSON struct {
	TicketParams ticketParamsJSON `json:"ticket_params"`
	ModelPrices  []modelJSON      `json:"model_prices"`
}

type ticketParamsJSON struct {
	Recipient         string                     `json:"recipient"`
	FaceValueWei      string                     `json:"face_value_wei"`
	WinProb           string                     `json:"win_prob"`
	RecipientRandHash string                     `json:"recipient_rand_hash"`
	Seed              string                     `json:"seed"`
	ExpirationBlock   string                     `json:"expiration_block"`
	ExpirationParams  ticketExpirationParamsJSON `json:"expiration_params"`
}

type ticketExpirationParamsJSON struct {
	CreationRound          int64  `json:"creation_round"`
	CreationRoundBlockHash string `json:"creation_round_block_hash"`
}

// fetchQuoteJSON hits PayeeDaemon.GetQuote and projects the result
// into the wire JSON shape. Returns (out, suggestedHTTPStatusOnError,
// err); on success the status is ignored by the caller.
func fetchQuoteJSON(ctx context.Context, payee payeedaemon.Client, sender []byte, capability string) (*quoteJSON, int, error) {
	res, err := payee.GetQuote(ctx, sender, capability)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	out := &quoteJSON{
		TicketParams: ticketParamsJSON{
			Recipient:         "0x" + hex.EncodeToString(res.TicketParams.Recipient),
			FaceValueWei:      "0x" + hex.EncodeToString(res.TicketParams.FaceValueWei),
			WinProb:           "0x" + hex.EncodeToString(res.TicketParams.WinProb),
			RecipientRandHash: "0x" + hex.EncodeToString(res.TicketParams.RecipientRandHash),
			Seed:              "0x" + hex.EncodeToString(res.TicketParams.Seed),
			ExpirationBlock:   "0x" + hex.EncodeToString(res.TicketParams.ExpirationBlock),
			ExpirationParams: ticketExpirationParamsJSON{
				CreationRound:          res.TicketParams.ExpirationParams.CreationRound,
				CreationRoundBlockHash: "0x" + hex.EncodeToString(res.TicketParams.ExpirationParams.CreationRoundBlockHash),
			},
		},
		ModelPrices: make([]modelJSON, 0, len(res.ModelPrices)),
	}
	for _, m := range res.ModelPrices {
		out.ModelPrices = append(out.ModelPrices, modelJSON{
			Model:               m.Model,
			PricePerWorkUnitWei: m.PricePerWorkUnitWei,
		})
	}
	return out, 0, nil
}
