package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Counter is a tiny test-only Recorder implementation that counts
// each method invocation and stores last-set values in maps. Other
// packages reuse this in tests so they don't have to redefine a
// Recorder stub for every package.
//
// Goroutine-safe: counters use atomic; map writes use the embedded mu.
//
// Not exported as part of the production API surface — file is
// _testhelpers.go-style but kept buildable so go vet runs against it.
type Counter struct {
	mu sync.Mutex

	Requests            atomic.Int64
	RequestObserves     atomic.Int64
	WorkUnitsAdds       atomic.Int64
	WorkUnitsTotal      atomic.Int64
	DaemonRPCCalls      atomic.Int64
	DaemonRPCObserves   atomic.Int64
	BackendRequests     atomic.Int64
	BackendObserves     atomic.Int64
	BackendErrors       atomic.Int64
	TokenizerCalls      atomic.Int64
	CapacityRejections  atomic.Int64
	PaymentRejections   atomic.Int64

	InflightV       atomic.Int64
	MaxConcurrentV  atomic.Int64

	// LastReason fields capture the most recent label value for
	// assertions in package tests.
	LastRequestOutcome     atomic.Value // string
	LastDaemonRPCMethod    atomic.Value // string
	LastDaemonRPCOutcome   atomic.Value // string
	LastBackendOutcome     atomic.Value // string
	LastBackendErrorClass  atomic.Value // string
	LastPaymentReason      atomic.Value // string
	LastTokenizerOutcome   atomic.Value // string
	LastWorkUnitsUnit      atomic.Value // string
}

// NewCounter returns a fresh Counter recorder.
func NewCounter() *Counter { return &Counter{} }

func (c *Counter) IncRequest(_, _, outcome string) {
	c.Requests.Add(1)
	c.LastRequestOutcome.Store(outcome)
}
func (c *Counter) ObserveRequest(_, _, _ string, _ time.Duration) {
	c.RequestObserves.Add(1)
}
func (c *Counter) AddWorkUnits(_, _, unit string, n int64) {
	c.WorkUnitsAdds.Add(1)
	c.WorkUnitsTotal.Add(n)
	c.LastWorkUnitsUnit.Store(unit)
}

func (c *Counter) IncDaemonRPC(method, outcome string) {
	c.DaemonRPCCalls.Add(1)
	c.LastDaemonRPCMethod.Store(method)
	c.LastDaemonRPCOutcome.Store(outcome)
}
func (c *Counter) ObserveDaemonRPC(_, _ string, _ time.Duration) {
	c.DaemonRPCObserves.Add(1)
}

func (c *Counter) IncBackendRequest(_, _, outcome string) {
	c.BackendRequests.Add(1)
	c.LastBackendOutcome.Store(outcome)
}
func (c *Counter) ObserveBackendRequest(_, _ string, _ time.Duration) {
	c.BackendObserves.Add(1)
}
func (c *Counter) IncBackendError(_, _, errorClass string) {
	c.BackendErrors.Add(1)
	c.LastBackendErrorClass.Store(errorClass)
}
func (c *Counter) SetBackendLastSuccess(_, _ string, _ time.Time) {}

func (c *Counter) IncTokenizerCall(_, outcome string) {
	c.TokenizerCalls.Add(1)
	c.LastTokenizerOutcome.Store(outcome)
}

func (c *Counter) IncCapacityRejection(_ string) { c.CapacityRejections.Add(1) }
func (c *Counter) SetInflightRequests(n int)     { c.InflightV.Store(int64(n)) }

func (c *Counter) IncPaymentRejection(reason string) {
	c.PaymentRejections.Add(1)
	c.LastPaymentReason.Store(reason)
}

func (c *Counter) SetUptimeSeconds(_ float64)                {}
func (c *Counter) SetBuildInfo(_ string, _ string, _ string) {}
func (c *Counter) SetMaxConcurrent(n int)                    { c.MaxConcurrentV.Store(int64(n)) }

func (c *Counter) Handler() http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
}
