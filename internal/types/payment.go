package types

// PaymentHeaderName is the HTTP header the bridge sends the
// base64-encoded `livepeer.payments.v1.Payment` bytes in. Mirrors the
// bridge's outgoing convention in openai-livepeer-bridge (see
// src/providers/nodeClient/fetch.ts).
const PaymentHeaderName = "livepeer-payment"

// WorkID is the session key the daemon uses to track per-(sender,
// session) balances. The worker derives it from the incoming payment
// so a given (sender, capability) pair collapses to one long-lived
// session across many requests — reflecting the library's
// RecipientRandHash-per-session model.
type WorkID string
