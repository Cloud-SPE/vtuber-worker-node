package metrics

import (
	"net/http"
	"time"
)

// Noop is the zero-cost Recorder implementation used when the worker
// is run without --metrics-listen. Every method returns immediately;
// the call site is one indirect call cheaper than checking for a nil
// Recorder.
//
// Handler returns 404 so an operator scraping a misconfigured port
// gets a clear "no metrics here" signal rather than a silent empty
// success.
type Noop struct{}

// NewNoop returns a Noop recorder. Pointer receiver throughout so
// future stateful additions don't change call sites.
func NewNoop() *Noop { return &Noop{} }

func (*Noop) IncRequest(_, _, _ string)                          {}
func (*Noop) ObserveRequest(_, _, _ string, _ time.Duration)     {}
func (*Noop) AddWorkUnits(_, _, _ string, _ int64)               {}
func (*Noop) IncDaemonRPC(_, _ string)                           {}
func (*Noop) ObserveDaemonRPC(_, _ string, _ time.Duration)      {}
func (*Noop) IncBackendRequest(_, _, _ string)                   {}
func (*Noop) ObserveBackendRequest(_, _ string, _ time.Duration) {}
func (*Noop) IncBackendError(_, _, _ string)                     {}
func (*Noop) SetBackendLastSuccess(_, _ string, _ time.Time)     {}
func (*Noop) IncTokenizerCall(_, _ string)                       {}
func (*Noop) IncCapacityRejection(_ string)                      {}
func (*Noop) SetInflightRequests(_ int)                          {}
func (*Noop) IncPaymentRejection(_ string)                       {}
func (*Noop) SetUptimeSeconds(_ float64)                         {}
func (*Noop) SetBuildInfo(_ string, _ string, _ string)          {}
func (*Noop) SetMaxConcurrent(_ int)                             {}

// Handler returns a 404-everywhere handler.
func (*Noop) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "metrics listener not enabled (start the worker with --metrics-listen)", http.StatusNotFound)
	})
}
