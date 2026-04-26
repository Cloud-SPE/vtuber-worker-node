// Package modules defines the contract that every capability module
// implements. A capability module is the business-logic half of a
// paid HTTP route: it owns the request/response schema, the work-unit
// estimation, and the dispatch to the backend inference server.
//
// The Module interface is intentionally minimal. It contains no
// payment logic — the runtime/http package owns that. Modules MUST
// NOT import payeedaemon, config, or any cross-cutting concern outside
// providers/; the middleware is what ties modules and payments
// together.
//
// Each capability ships as its own sub-package (chat_completions,
// embeddings, images, audio_speech, audio_transcriptions, etc.). The
// Module returned from a capability package's Register* call is
// handed to runtime/http.Mux.RegisterPaidRoute, which wraps it in the
// payment middleware.
package modules
