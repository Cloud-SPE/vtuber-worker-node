package payeedaemon

import "math/big"

// ProcessPaymentResult is the domain projection of
// paymentsv1.ProcessPaymentResponse. Wei values are *big.Int because
// every consumer (middleware reconciliation, balance checks, logs)
// needs to compare and arithmetic on them; exposing big-endian bytes
// would just push the same conversion into every call site.
type ProcessPaymentResult struct {
	// Sender address (20 bytes, as returned by the daemon).
	Sender []byte
	// CreditedEVWei is the expected value credited to the sender's
	// balance by this payment.
	CreditedEVWei *big.Int
	// BalanceWei is the sender's new balance after this credit.
	BalanceWei *big.Int
	// WinnersQueued is the count of winning tickets the daemon queued
	// for on-chain redemption.
	WinnersQueued int32
}

// DebitBalanceResult is the domain projection of
// paymentsv1.DebitBalanceResponse.
type DebitBalanceResult struct {
	// BalanceWei is the sender's balance after the debit. Callers MUST
	// treat a negative balance as insufficient and refuse to serve
	// further work; the daemon itself does not gate on this.
	BalanceWei *big.Int
}

// ListCapabilitiesResult mirrors paymentsv1.ListCapabilitiesResponse
// in domain types. Used at startup to cross-check against the worker's
// own worker.yaml parse.
type ListCapabilitiesResult struct {
	Capabilities []Capability
}

// Capability mirrors paymentsv1.CapabilityEntry.
type Capability struct {
	// Capability is the canonical capability string, e.g.
	// "openai:/v1/chat/completions".
	Capability string
	// WorkUnit identifies the metering unit ("token", "audio_second",
	// ...). Opaque to the daemon; used by modules + observability.
	WorkUnit string
	// Offerings is the list of offerings served on this capability,
	// each with its configured per-unit price. Ordered as the daemon
	// emits them (capability string, then offering id).
	Offerings []OfferingPrice
}

// OfferingPrice mirrors paymentsv1.OfferingPrice.
type OfferingPrice struct {
	// Offering identifier (e.g. "vtuber-default-1080p30").
	ID string
	// PricePerWorkUnitWei, as a decimal string. Retained as a string
	// so byte-equal comparison against the worker's worker.yaml parse
	// parse is exact — no rounding, no scientific-notation drift.
	PricePerWorkUnitWei string
}

// GetQuoteResult is the domain projection of
// paymentsv1.GetQuoteResponse. TicketParams is passed through as
// flat bytes-and-numbers so the HTTP handler can render it into the
// JSON shape the bridge expects without touching proto types.
type GetQuoteResult struct {
	TicketParams   TicketParams
	OfferingPrices []OfferingPrice
}

// GetTicketParamsRequest is the worker-side projection of the
// daemon's exact ticket-params request.
type GetTicketParamsRequest struct {
	Sender     []byte
	Recipient  []byte
	FaceValue  *big.Int
	Capability string
	Offering   string
}

// TicketParams is the worker-side projection of the proto TicketParams.
// Byte fields are passed through unchanged; the HTTP handler is the
// only layer that renders them (as hex strings, per the bridge's
// JSON schema).
type TicketParams struct {
	Recipient         []byte
	FaceValueWei      []byte // big-endian
	WinProb           []byte // big-endian
	RecipientRandHash []byte
	Seed              []byte
	ExpirationBlock   []byte // big-endian
	ExpirationParams  TicketExpirationParams
}

// TicketExpirationParams projects paymentsv1.TicketExpirationParams.
type TicketExpirationParams struct {
	CreationRound          int64
	CreationRoundBlockHash []byte
}

// SufficientBalanceResult is the domain projection of
// paymentsv1.SufficientBalanceResponse.
type SufficientBalanceResult struct {
	// Sufficient is true when (sender, workID) has at least the
	// caller-supplied minWorkUnits of remaining balance. The streaming-
	// session-payment pattern uses this as a watermark check between
	// Debit ticks; see livepeer-modules-project/payment-daemon/docs/design-docs/
	// streaming-session-pattern.md.
	Sufficient bool
	// BalanceWei is the sender's current balance.
	BalanceWei *big.Int
}
