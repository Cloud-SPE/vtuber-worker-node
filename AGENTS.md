# AGENTS.md — vtuber-worker-node

Map for agents working in this repo. Keep under ~150 lines.

## What this repo is

The payee-side adapter for the **`livepeer:vtuber-session`** capability. Hosts the [`StreamingModule`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/streaming-session-module.md) interface, runs payment middleware via a co-located [`livepeer-modules-project/payment-daemon`](https://github.com/Cloud-SPE/livepeer-modules-project/tree/main/payment-daemon) receiver daemon, publishes its capability via [`livepeer-modules-project/service-registry-daemon`](https://github.com/Cloud-SPE/livepeer-modules-project/tree/main/service-registry-daemon), forwards session-open requests to its local [`session-runner`](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/session-runner) backend.

Sibling of [`openai-worker-node`](https://github.com/Cloud-SPE/openai-worker-node) — structurally shaped after it; no shared source. See [ADR-003](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/003-sibling-project-integration.md) for the integration shape.

## Where to look for what

| If you need... | Start here |
|---|---|
| What this project is | [`README.md`](README.md) |
| The build plan against this repo | [vtuber-worker-node-bootstrap.md (upstream)](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/exec-plans/active/vtuber-worker-node-bootstrap.md) |
| The interface this repo implements | [streaming-session-module.md (upstream)](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/streaming-session-module.md) |
| Why a major design call was made | [ADRs (upstream)](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/docs/design-docs/decisions) |
| How sibling repos slot in | [sibling-integration.md (upstream)](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/sibling-integration.md) |
| Cross-repo conventions (metrics, ports) | [livepeer-modules-conventions](https://github.com/Cloud-SPE/livepeer-modules-conventions) |
| Repo layout + status | [`README.md`](README.md) |

## Ground rules

1. **No manually-written code.** Humans write plans and reviews. Agents write every line of code, test, config, CI.
2. **No shared source with `openai-worker-node`.** Search-and-replace baseline only. Common patterns are documented (in the upstream `sibling-integration.md`); they are NOT shared as a Go module.
3. **Layer rule.** Capability modules live under `internal/service/modules/<capability>/`. They MUST NOT import `payeedaemon`, `config`, or any cross-cutting concern outside `providers/`. The `runtime/http` middleware is the seam between payments and modules. Enforced by the custom `lint/payment-middleware-check` analyzer.
4. **Forbidden vocabulary.** Per [ADR-001](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/001-drop-byoc-terminology.md) + [ADR-002](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/002-drop-trickle-adopt-websocket-and-chunked-post.md): no `BYOC`, no `/process/stream/start`, no standalone `trickle` (the library `pytrickle` is fine — but this repo is Go and doesn't use it). The upstream `livepeer-vtuber-project` enforces this lint; this repo inherits the convention.
5. **Cross-repo asks land in the upstream `sibling-coordination.md`.** If a change here needs a corresponding change in a sibling repo, log the ask there — not in a Slack thread.

## Build / test / lint

```
go build -o bin/vtuber-worker-node ./cmd/vtuber-worker-node
go test ./...
make lint
```

CI runs all three on every PR.

## Current status

M1 of [`vtuber-worker-node-bootstrap`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/exec-plans/active/vtuber-worker-node-bootstrap.md) — skeleton only. M2 lands the `StreamingModule` interface; M3 the `vtuber-session` module; M4 the contract tests. See the upstream plan for the full milestone list.
