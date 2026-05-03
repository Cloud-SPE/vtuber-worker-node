package payeedaemon

import (
	"context"
	"errors"
	"math/big"
	"sync"
)

// Fake is an in-memory Client for tests. Thread-safe (mu-guarded) so
// middleware tests can run ProcessPayment / DebitBalance on goroutines
// without racing.
//
// Behavior knobs:
//
//   - ProcessPaymentError: if non-nil, ProcessPayment returns it.
//   - DebitBalanceError:   if non-nil, DebitBalance returns it.
//   - ListCapabilitiesResponse: what ListCapabilities emits.
//   - CreditPerCall: wei credited on each ProcessPayment (default 1e9).
//   - DebitBytesToWei: rate to debit per work unit (default 1 wei / unit).
//
// Use zero-value + setters; the Fake is concurrency-safe for all ops.
type Fake struct {
	mu sync.Mutex

	ProcessPaymentError       error
	OpenSessionError          error
	DebitBalanceError         error
	SufficientBalanceError    error
	CloseSessionError         error
	ListCapabilitiesResponse  ListCapabilitiesResult
	ListCapabilitiesError     error
	GetQuoteResponse          GetQuoteResult
	GetQuoteError             error
	GetTicketParamsResponse   TicketParams
	GetTicketParamsError      error
	CreditPerCall             *big.Int
	DebitWeiPerWorkUnit       *big.Int
	SenderAddress             []byte
	OpenSessionCalls          int
	ProcessPaymentCalls       int
	DebitBalanceCalls         int
	SufficientBalanceCalls    int
	CloseSessionCalls         int
	GetQuoteCalls             int
	GetTicketParamsCalls      int
	LastOpenSession           OpenSessionRequest
	LastProcessPaymentPayload []byte
	LastDebitBalanceWorkUnits int64
	LastDebitBalanceSeq       uint64
	LastProcessPaymentWorkID  string
	LastDebitBalanceWorkID    string
	LastSufficientWorkID      string
	LastSufficientMinUnits    int64
	LastCloseSessionWorkID    string
	LastGetQuoteSender        []byte
	LastGetQuoteCapability    string
	LastGetTicketParams       GetTicketParamsRequest

	// balances tracks (sender, work_id) → running balance so the fake
	// stays consistent across calls (e.g. insufficient-balance tests).
	balances map[string]*big.Int

	// sessions tracks the authoritative session binding by workID until
	// the first successful ProcessPayment fixes the sender.
	sessions map[string]fakeSessionBinding

	// debitReplays records the post-debit balance for an idempotent
	// (sender, workID, debitSeq) tuple.
	debitReplays map[string]*big.Int
}

type fakeSessionBinding struct {
	Capability          string
	Offering            string
	PricePerWorkUnitWei *big.Int
	WorkUnit            string
	Sender              []byte
	Closed              bool
}

// NewFake returns a ready-to-use fake with sensible defaults.
func NewFake() *Fake {
	return &Fake{
		CreditPerCall:       new(big.Int).SetInt64(1_000_000_000),
		DebitWeiPerWorkUnit: new(big.Int).SetInt64(1),
		SenderAddress:       []byte{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d},
		balances:            map[string]*big.Int{},
		sessions:            map[string]fakeSessionBinding{},
		debitReplays:        map[string]*big.Int{},
	}
}

func (f *Fake) Close() error { return nil }

func (f *Fake) ListCapabilities(_ context.Context) (ListCapabilitiesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListCapabilitiesError != nil {
		return ListCapabilitiesResult{}, f.ListCapabilitiesError
	}
	return f.ListCapabilitiesResponse, nil
}

func (f *Fake) GetQuote(_ context.Context, sender []byte, capability string) (GetQuoteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.GetQuoteCalls++
	f.LastGetQuoteSender = append([]byte(nil), sender...)
	f.LastGetQuoteCapability = capability
	if f.GetQuoteError != nil {
		return GetQuoteResult{}, f.GetQuoteError
	}
	return f.GetQuoteResponse, nil
}

func (f *Fake) GetTicketParams(_ context.Context, req GetTicketParamsRequest) (TicketParams, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.GetTicketParamsCalls++
	f.LastGetTicketParams = GetTicketParamsRequest{
		Sender:     append([]byte(nil), req.Sender...),
		Recipient:  append([]byte(nil), req.Recipient...),
		FaceValue:  new(big.Int).Set(req.FaceValue),
		Capability: req.Capability,
		Offering:   req.Offering,
	}
	if f.GetTicketParamsError != nil {
		return TicketParams{}, f.GetTicketParamsError
	}
	return f.GetTicketParamsResponse, nil
}

func (f *Fake) OpenSession(_ context.Context, req OpenSessionRequest) (OpenSessionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.OpenSessionCalls++
	f.LastOpenSession = cloneOpenSessionRequest(req)
	if f.OpenSessionError != nil {
		return OpenSessionResult{}, f.OpenSessionError
	}
	existing, ok := f.sessions[req.WorkID]
	if ok {
		if existing.Closed {
			return OpenSessionResult{}, errors.New("session closed")
		}
		if sameSessionBinding(existing, req) {
			return OpenSessionResult{Outcome: OpenSessionOutcomeAlreadyOpen}, nil
		}
		return OpenSessionResult{}, errors.New("session already open with different metadata")
	}
	f.sessions[req.WorkID] = fakeSessionBinding{
		Capability:          req.Capability,
		Offering:            req.Offering,
		PricePerWorkUnitWei: cloneBigInt(req.PricePerWorkUnitWei),
		WorkUnit:            req.WorkUnit,
	}
	return OpenSessionResult{Outcome: OpenSessionOutcomeOpened}, nil
}

func (f *Fake) ProcessPayment(_ context.Context, paymentBytes []byte, workID string) (ProcessPaymentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProcessPaymentCalls++
	f.LastProcessPaymentPayload = append([]byte(nil), paymentBytes...)
	f.LastProcessPaymentWorkID = workID
	if f.ProcessPaymentError != nil {
		return ProcessPaymentResult{}, f.ProcessPaymentError
	}
	session, ok := f.sessions[workID]
	if !ok {
		return ProcessPaymentResult{}, errors.New("unknown session")
	}
	if session.Closed {
		return ProcessPaymentResult{}, errors.New("session closed")
	}
	if len(session.Sender) == 0 {
		session.Sender = append([]byte(nil), f.SenderAddress...)
		f.sessions[workID] = session
	} else if string(session.Sender) != string(f.SenderAddress) {
		return ProcessPaymentResult{}, errors.New("sender mismatch")
	}
	credited := new(big.Int).Set(f.CreditPerCall)
	key := balanceKey(f.SenderAddress, workID)
	bal, ok := f.balances[key]
	if !ok {
		bal = new(big.Int)
	}
	bal.Add(bal, credited)
	f.balances[key] = bal
	return ProcessPaymentResult{
		Sender:        append([]byte(nil), f.SenderAddress...),
		CreditedEVWei: credited,
		BalanceWei:    new(big.Int).Set(bal),
	}, nil
}

func (f *Fake) DebitBalance(_ context.Context, sender []byte, workID string, workUnits int64, debitSeq uint64) (DebitBalanceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DebitBalanceCalls++
	f.LastDebitBalanceWorkUnits = workUnits
	f.LastDebitBalanceWorkID = workID
	f.LastDebitBalanceSeq = debitSeq
	if f.DebitBalanceError != nil {
		return DebitBalanceResult{}, f.DebitBalanceError
	}
	session, ok := f.sessions[workID]
	if !ok {
		return DebitBalanceResult{}, errors.New("unknown session")
	}
	if session.Closed {
		return DebitBalanceResult{}, errors.New("session closed")
	}
	if len(session.Sender) == 0 || string(session.Sender) != string(sender) {
		return DebitBalanceResult{}, errors.New("sender mismatch")
	}
	replayKey := debitReplayKey(sender, workID, debitSeq)
	if bal, ok := f.debitReplays[replayKey]; ok {
		return DebitBalanceResult{BalanceWei: new(big.Int).Set(bal)}, nil
	}
	key := balanceKey(sender, workID)
	bal, ok := f.balances[key]
	if !ok {
		bal = new(big.Int)
	}
	price := f.DebitWeiPerWorkUnit
	if session.PricePerWorkUnitWei != nil {
		price = session.PricePerWorkUnitWei
	}
	debit := new(big.Int).Mul(price, big.NewInt(workUnits))
	bal.Sub(bal, debit)
	f.balances[key] = bal
	f.debitReplays[replayKey] = new(big.Int).Set(bal)
	return DebitBalanceResult{BalanceWei: new(big.Int).Set(bal)}, nil
}

func (f *Fake) SufficientBalance(_ context.Context, sender []byte, workID string, minWorkUnits int64) (SufficientBalanceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SufficientBalanceCalls++
	f.LastSufficientWorkID = workID
	f.LastSufficientMinUnits = minWorkUnits
	if f.SufficientBalanceError != nil {
		return SufficientBalanceResult{}, f.SufficientBalanceError
	}
	session, ok := f.sessions[workID]
	if !ok {
		return SufficientBalanceResult{}, errors.New("unknown session")
	}
	if session.Closed {
		return SufficientBalanceResult{}, errors.New("session closed")
	}
	if len(session.Sender) == 0 || string(session.Sender) != string(sender) {
		return SufficientBalanceResult{}, errors.New("sender mismatch")
	}
	key := balanceKey(sender, workID)
	bal, ok := f.balances[key]
	if !ok {
		bal = new(big.Int)
	}
	price := f.DebitWeiPerWorkUnit
	if session.PricePerWorkUnitWei != nil {
		price = session.PricePerWorkUnitWei
	}
	min := new(big.Int).Mul(price, big.NewInt(minWorkUnits))
	return SufficientBalanceResult{
		Sufficient: bal.Cmp(min) >= 0,
		BalanceWei: new(big.Int).Set(bal),
	}, nil
}

func (f *Fake) CloseSession(_ context.Context, sender []byte, workID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CloseSessionCalls++
	f.LastCloseSessionWorkID = workID
	if f.CloseSessionError != nil {
		return f.CloseSessionError
	}
	session, ok := f.sessions[workID]
	if !ok {
		return errors.New("unknown session")
	}
	if len(session.Sender) > 0 && string(session.Sender) != string(sender) {
		return errors.New("sender mismatch")
	}
	session.Closed = true
	f.sessions[workID] = session
	return nil
}

// BalanceFor returns the current balance for a (sender, workID) pair.
// Used by tests to assert ledger state after a sequence of calls.
func (f *Fake) BalanceFor(sender []byte, workID string) *big.Int {
	f.mu.Lock()
	defer f.mu.Unlock()
	bal, ok := f.balances[balanceKey(sender, workID)]
	if !ok {
		return new(big.Int)
	}
	return new(big.Int).Set(bal)
}

func balanceKey(sender []byte, workID string) string {
	return string(sender) + "::" + workID
}

func debitReplayKey(sender []byte, workID string, debitSeq uint64) string {
	return balanceKey(sender, workID) + "::" + big.NewInt(0).SetUint64(debitSeq).String()
}

func sameSessionBinding(existing fakeSessionBinding, req OpenSessionRequest) bool {
	if existing.Capability != req.Capability || existing.Offering != req.Offering || existing.WorkUnit != req.WorkUnit {
		return false
	}
	switch {
	case existing.PricePerWorkUnitWei == nil && req.PricePerWorkUnitWei == nil:
		return true
	case existing.PricePerWorkUnitWei == nil || req.PricePerWorkUnitWei == nil:
		return false
	default:
		return existing.PricePerWorkUnitWei.Cmp(req.PricePerWorkUnitWei) == 0
	}
}

func cloneOpenSessionRequest(req OpenSessionRequest) OpenSessionRequest {
	return OpenSessionRequest{
		WorkID:              req.WorkID,
		Capability:          req.Capability,
		Offering:            req.Offering,
		PricePerWorkUnitWei: cloneBigInt(req.PricePerWorkUnitWei),
		WorkUnit:            req.WorkUnit,
	}
}

func cloneBigInt(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}
