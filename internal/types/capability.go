package types

// CapabilityID is the canonical capability string in the form
// `<domain>:<identifier>`, e.g. `openai:/v1/chat/completions`. Matches
// the library's regex `^[a-z][a-z0-9]*:.+$` — validation lives in
// sharedyaml; this type is a named alias for clarity at call sites.
type CapabilityID string

// ModelID is the model identifier a module routes on, e.g.
// `llama-3.3-70b`. Opaque to the worker framework; each module decides
// what it accepts.
type ModelID string

// WorkUnit identifies the metering unit for a capability. The library
// treats it as opaque int64 at DebitBalance; this typed constant set
// is for clarity in worker code and logs only.
type WorkUnit string

const (
	WorkUnitToken               WorkUnit = "token"
	WorkUnitCharacter           WorkUnit = "character"
	WorkUnitAudioSecond         WorkUnit = "audio_second"
	WorkUnitImageStepMegapixel  WorkUnit = "image_step_megapixel"
	WorkUnitVideoFrameMegapixel WorkUnit = "video_frame_megapixel"
)
