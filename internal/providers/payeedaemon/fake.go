package payeedaemon

import (
	"context"
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
	ProcessPaymentCalls       int
	DebitBalanceCalls         int
	SufficientBalanceCalls    int
	CloseSessionCalls         int
	GetQuoteCalls             int
	GetTicketParamsCalls      int
	LastProcessPaymentPayload []byte
	LastDebitBalanceWorkUnits int64
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
}

// NewFake returns a ready-to-use fake with sensible defaults.
func NewFake() *Fake {
	return &Fake{
		CreditPerCall:       new(big.Int).SetInt64(1_000_000_000),
		DebitWeiPerWorkUnit: new(big.Int).SetInt64(1),
		SenderAddress:       []byte{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d},
		balances:            map[string]*big.Int{},
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

func (f *Fake) ProcessPayment(_ context.Context, paymentBytes []byte, workID string) (ProcessPaymentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProcessPaymentCalls++
	f.LastProcessPaymentPayload = append([]byte(nil), paymentBytes...)
	f.LastProcessPaymentWorkID = workID
	if f.ProcessPaymentError != nil {
		return ProcessPaymentResult{}, f.ProcessPaymentError
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

func (f *Fake) DebitBalance(_ context.Context, sender []byte, workID string, workUnits int64) (DebitBalanceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DebitBalanceCalls++
	f.LastDebitBalanceWorkUnits = workUnits
	f.LastDebitBalanceWorkID = workID
	if f.DebitBalanceError != nil {
		return DebitBalanceResult{}, f.DebitBalanceError
	}
	key := balanceKey(sender, workID)
	bal, ok := f.balances[key]
	if !ok {
		bal = new(big.Int)
	}
	debit := new(big.Int).Mul(f.DebitWeiPerWorkUnit, big.NewInt(workUnits))
	bal.Sub(bal, debit)
	f.balances[key] = bal
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
	key := balanceKey(sender, workID)
	bal, ok := f.balances[key]
	if !ok {
		bal = new(big.Int)
	}
	min := new(big.Int).Mul(f.DebitWeiPerWorkUnit, big.NewInt(minWorkUnits))
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
	delete(f.balances, balanceKey(sender, workID))
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
