package http

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/config"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/providers/payeedaemon"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ethAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

const maxTicketParamsBodyBytes = 8 << 10 // 8 KiB

// RegisterUnpaidHandlers binds the standard unpaid routes on mux.
func RegisterUnpaidHandlers(m *Mux, cfg *config.Config) {
	m.Register(http.MethodGet, "/health", healthHandler(cfg, m))
	m.Register(http.MethodGet, "/registry/offerings", registryOfferingsHandler(cfg))
	m.Register(http.MethodPost, "/v1/payment/ticket-params", ticketParamsHandler(cfg, m.payee))
}

func healthHandler(cfg *config.Config, mux *Mux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":           "ok",
			"api_version":      cfg.APIVersion,
			"protocol_version": cfg.ProtocolVersion,
			"max_concurrent":   mux.MaxConcurrentPaid(),
			"inflight":         mux.InflightPaid(),
		})
	}
}

type offeringJSON struct {
	ID                  string `json:"id"`
	PricePerWorkUnitWei string `json:"price_per_work_unit_wei"`
}

type registryOfferingsJSON struct {
	WorkerEthAddress string                   `json:"worker_eth_address,omitempty"`
	Capabilities     []registryCapabilityJSON `json:"capabilities"`
}

type registryCapabilityJSON struct {
	Name      string         `json:"name"`
	WorkUnit  string         `json:"work_unit"`
	Offerings []offeringJSON `json:"offerings"`
}

func registryOfferingsHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireBearerAuth(w, r, cfg.AuthToken) {
			return
		}
		out := registryOfferingsJSON{
			Capabilities: make([]registryCapabilityJSON, 0, len(cfg.Capabilities.Ordered)),
		}
		if cfg.WorkerEthAddress != "" {
			out.WorkerEthAddress = cfg.WorkerEthAddress
		}
		for _, c := range cfg.Capabilities.Ordered {
			offerings := make([]offeringJSON, 0, len(c.Offerings))
			for _, o := range c.Offerings {
				offerings = append(offerings, offeringJSON{
					ID:                  string(o.ID),
					PricePerWorkUnitWei: o.PricePerWorkUnitWei,
				})
			}
			out.Capabilities = append(out.Capabilities, registryCapabilityJSON{
				Name:      string(c.Capability),
				WorkUnit:  string(c.WorkUnit),
				Offerings: offerings,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

type ticketParamsRequestJSON struct {
	SenderETHAddress    string `json:"sender_eth_address"`
	RecipientETHAddress string `json:"recipient_eth_address"`
	FaceValueWei        string `json:"face_value_wei"`
	Capability          string `json:"capability"`
	Offering            string `json:"offering"`
}

type ticketParamsResponseJSON struct {
	TicketParams ticketParamsJSON `json:"ticket_params"`
}

type ticketParamsJSON struct {
	Recipient         string                     `json:"recipient"`
	FaceValue         string                     `json:"face_value"`
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

func ticketParamsHandler(cfg *config.Config, payee payeedaemon.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireBearerAuth(w, r, cfg.AuthToken) {
			return
		}
		defer func() { _ = r.Body.Close() }()

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTicketParamsBodyBytes))
		dec.DisallowUnknownFields()

		var req ticketParamsRequestJSON
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+err.Error())
			return
		}
		if err := ensureSingleJSONDocument(dec); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}

		daemonReq, err := parseTicketParamsRequest(req)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}

		params, err := payee.GetTicketParams(r.Context(), daemonReq)
		if err != nil {
			writeTicketParamsProxyError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ticketParamsResponseJSON{
			TicketParams: renderTicketParamsJSON(params),
		})
	}
}

func requireBearerAuth(w http.ResponseWriter, r *http.Request, authToken string) bool {
	if authToken == "" {
		return true
	}
	gotAuth := r.Header.Get("Authorization")
	want := "Bearer " + authToken
	if subtle.ConstantTimeCompare([]byte(gotAuth), []byte(want)) != 1 {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
		return false
	}
	return true
}

func ensureSingleJSONDocument(dec *json.Decoder) error {
	var tail struct{}
	if err := dec.Decode(&tail); err == nil {
		return fmt.Errorf("request body must contain exactly one JSON object")
	} else if err == io.EOF {
		return nil
	} else {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
}

func parseHexAddress(field, value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if !ethAddressRE.MatchString(value) {
		return nil, fmt.Errorf("%s must be a 0x-prefixed 40-hex address", field)
	}
	b, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, fmt.Errorf("%s decode: %w", field, err)
	}
	return b, nil
}

func parseTicketParamsRequest(in ticketParamsRequestJSON) (payeedaemon.GetTicketParamsRequest, error) {
	sender, err := parseHexAddress("sender_eth_address", in.SenderETHAddress)
	if err != nil {
		return payeedaemon.GetTicketParamsRequest{}, err
	}
	recipient, err := parseHexAddress("recipient_eth_address", in.RecipientETHAddress)
	if err != nil {
		return payeedaemon.GetTicketParamsRequest{}, err
	}
	faceValue, ok := new(big.Int).SetString(strings.TrimSpace(in.FaceValueWei), 10)
	if !ok {
		return payeedaemon.GetTicketParamsRequest{}, fmt.Errorf("face_value_wei must be a decimal integer")
	}
	if faceValue.Sign() <= 0 {
		return payeedaemon.GetTicketParamsRequest{}, fmt.Errorf("face_value_wei must be > 0")
	}
	if strings.TrimSpace(in.Capability) == "" {
		return payeedaemon.GetTicketParamsRequest{}, fmt.Errorf("capability is required")
	}
	if strings.TrimSpace(in.Offering) == "" {
		return payeedaemon.GetTicketParamsRequest{}, fmt.Errorf("offering is required")
	}
	return payeedaemon.GetTicketParamsRequest{
		Sender:     sender,
		Recipient:  recipient,
		FaceValue:  faceValue,
		Capability: strings.TrimSpace(in.Capability),
		Offering:   strings.TrimSpace(in.Offering),
	}, nil
}

func renderTicketParamsJSON(params payeedaemon.TicketParams) ticketParamsJSON {
	return ticketParamsJSON{
		Recipient:         bytesToHexString(params.Recipient),
		FaceValue:         bytesToDecimalString(params.FaceValueWei),
		WinProb:           bytesToHexString(params.WinProb),
		RecipientRandHash: bytesToHexString(params.RecipientRandHash),
		Seed:              bytesToHexString(params.Seed),
		ExpirationBlock:   bytesToDecimalString(params.ExpirationBlock),
		ExpirationParams: ticketExpirationParamsJSON{
			CreationRound:          params.ExpirationParams.CreationRound,
			CreationRoundBlockHash: bytesToHexString(params.ExpirationParams.CreationRoundBlockHash),
		},
	}
}

func bytesToHexString(b []byte) string {
	if len(b) == 0 {
		return "0x"
	}
	return "0x" + hex.EncodeToString(b)
}

func bytesToDecimalString(b []byte) string {
	if len(b) == 0 {
		return "0"
	}
	return new(big.Int).SetBytes(b).String()
}

func writeTicketParamsProxyError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "ticket_params_unavailable", err.Error())
		return
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		writeJSONError(w, http.StatusServiceUnavailable, "payment_daemon_unavailable", st.Message())
	case codes.InvalidArgument, codes.NotFound, codes.FailedPrecondition:
		writeJSONError(w, http.StatusBadRequest, "invalid_request", st.Message())
	default:
		writeJSONError(w, http.StatusInternalServerError, "ticket_params_unavailable", st.Message())
	}
}
