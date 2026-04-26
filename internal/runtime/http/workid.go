package http

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// deriveWorkID builds a stable WorkID from the raw payment bytes. Two
// requests carrying the same payment blob map to the same WorkID and
// therefore the same daemon balance; distinct blobs get distinct
// WorkIDs.
//
// The daemon treats work_id as opaque (for logs and idempotency), and
// derives its own session key (RecipientRandHash) from the payment's
// ticket_params. So the only property we need from a WorkID is "stable
// for a given blob". SHA-256 hex matches that and doesn't need a
// shared secret with the bridge.
//
// Future improvement: once the middleware unmarshals the Payment proto
// itself (rather than passing bytes through), derive work_id from the
// RecipientRandHash for byte-parity with the daemon's internal key.
// Tracked in tech-debt.
func deriveWorkID(paymentBytes []byte) types.WorkID {
	sum := sha256.Sum256(paymentBytes)
	return types.WorkID(hex.EncodeToString(sum[:]))
}
