# DESIGN.md — vtuber-worker-node

This file is intentionally short. The full design lives upstream at [`livepeer-vtuber-project`](https://github.com/Cloud-SPE/livepeer-vtuber-project).

## One-paragraph summary

`vtuber-worker-node` terminates session-open requests from `vtuber-livepeer-bridge`, validates the attached payment via a co-located [`livepeer-modules-project/payment-daemon`](https://github.com/Cloud-SPE/livepeer-modules-project/tree/main/payment-daemon) receiver daemon, instantiates a `StreamingModule` for the requested capability, and forwards to the local [`session-runner`](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/session-runner) backend over localhost HTTP. The module owns the session lifetime: it debits balance every 5s via `paymentSession.Debit`, emits `session.balance.low` on the WebSocket back to the bridge if `paymentSession.Sufficient` returns false, and calls `paymentSession.Close` exactly once before returning.

## Position in the stack

```
vtuber-livepeer-bridge (sibling repo)
   ↓ POST /api/sessions/start, Bearer customer key, Payment header
vtuber-worker-node (this repo)
   ├─ unix-socket gRPC ↓
   │   payment-daemon (receiver daemon, sidecar)
   ├─ unix-socket gRPC ↓
   │   service-registry-daemon (publisher daemon, sidecar)
   └─ HTTP localhost ↓
       session-runner (livepeer-vtuber-project)
```

This repo does **not** transcode, render, or hold session state. The session-runner does that. This repo's job is payment-middleware + capability-routing + module lifecycle.

## Layered domain architecture

Same convention as `openai-worker-node`. Inside `internal/`:

```
types/        # plain data shapes
config/       # parsed worker.yaml + daemon-catalog cross-check
repo/         # (empty — no persistence in this domain)
service/      # business logic (capability modules)
runtime/      # process entry, mux, payment middleware, /metrics
providers/    # cross-cutting (payment-daemon client, backend HTTP, recorder)
```

The custom analyzer `lint/payment-middleware-check` enforces that capability modules cannot bypass the middleware.

## Where the deeper docs live

- [System architecture overview](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/architecture-overview.md)
- [`StreamingModule` interface + lifecycle + state machine](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/streaming-session-module.md)
- [Streaming-session payment pattern](https://github.com/Cloud-SPE/livepeer-modules-project/blob/main/payment-daemon/docs/design-docs/streaming-session-pattern.md) — payment-library's recipe this module composes from
- [ADRs](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/docs/design-docs/decisions)
- [Cross-repo conventions](https://github.com/Cloud-SPE/livepeer-modules-conventions) (metrics naming, port allocations)

## What changes from `openai-worker-node`

| Concern | `openai-worker-node` | `vtuber-worker-node` |
|---|---|---|
| Module interface | `Module` (request/response) | `StreamingModule` (long-lived; periodic Debit) |
| Backend | OpenAI-compatible inference servers (vLLM, Ollama, …) | local `session-runner` (Python, Chromium + VRM) |
| Capability namespace | `openai:/v1/*` | `livepeer:vtuber-session` |
| Service-registry publisher | not yet (deferred per [ADR-009](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/009-vtuber-leads-service-registry-adoption.md)) | co-located from day one |
| Tokenizer | bundled (tiktoken) | not used (work-unit is `second`, not `token`) |

Both repos share `livepeer-modules-project/payment-daemon` (receiver mode) and the `worker.yaml` schema.
