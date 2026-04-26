# vtuber-worker-node

Payee-side HTTP adapter for the **`livepeer:vtuber-session`** capability. Hosts the [`StreamingModule`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/streaming-session-module.md) interface, runs the payment middleware via a co-located [`livepeer-modules-project/payment-daemon`](https://github.com/Cloud-SPE/livepeer-modules-project/tree/main/payment-daemon) receiver daemon, publishes its capability via a co-located [`livepeer-modules-project/service-registry-daemon`](https://github.com/Cloud-SPE/livepeer-modules-project/tree/main/service-registry-daemon) publisher daemon, and forwards session-open requests to its local [`session-runner`](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/session-runner) backend.

Sibling repo of [`openai-worker-node`](https://github.com/Cloud-SPE/openai-worker-node); structurally shaped after it. Per [ADR-003 in livepeer-vtuber-project](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/003-sibling-project-integration.md), the two repos share no source code — common patterns are documented in the [shared-patterns table](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/sibling-integration.md).

## Status

**M1 — repo scaffolding (skeleton only).** This commit copies the `openai-worker-node` skeleton and strips out OpenAI-specific module code. The `StreamingModule` interface, the `vtuber-session` module implementation, and the contract tests land in M2–M4. A binary built at this commit refuses to start against any non-empty `worker.yaml` — that's the intended state for skeleton acceptance. See the build plan: [`vtuber-worker-node-bootstrap.md`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/exec-plans/active/vtuber-worker-node-bootstrap.md).

## Repo layout

```
cmd/vtuber-worker-node/   # binary entrypoint
internal/
  config/                  # worker.yaml loader + daemon-catalog cross-check
  providers/
    backendhttp/           # HTTP client to the local session-runner backend
    metrics/               # Prometheus recorder + Noop recorder
    payeedaemon/           # gRPC client to payment-daemon (receiver mode)
  runtime/
    http/                  # mux + payment middleware
    metrics/               # /metrics listener
  service/modules/         # capability modules (currently empty; vtuber-session lands in M3)
  types/                   # CapabilityID, ModelID, etc.
lint/payment-middleware-check/  # custom golangci-lint analyzer
worker.example.yaml        # annotated example config
compose.yaml               # dev compose: payment-daemon + worker
compose.prod.yaml          # production compose template
Dockerfile                 # build artifact
```

## Build + test

```bash
go build -o bin/vtuber-worker-node ./cmd/vtuber-worker-node
go test ./...
make lint                # golangci-lint + custom payment-middleware-check
```

## Where the design lives

This repo's `docs/` is intentionally minimal until per-repo specifics emerge. The canonical design lives upstream in [`livepeer-vtuber-project`](https://github.com/Cloud-SPE/livepeer-vtuber-project):

- [System architecture overview](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/architecture-overview.md)
- [`StreamingModule` interface + lifecycle](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/streaming-session-module.md)
- [ADRs](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/docs/design-docs/decisions) — especially [ADR-006 (streaming-session payment pattern)](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/006-streaming-session-payment-pattern.md) and [ADR-009 (vtuber leads service-registry adoption)](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/009-vtuber-leads-service-registry-adoption.md)
- [Build plan: vtuber-worker-node-bootstrap](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/exec-plans/active/vtuber-worker-node-bootstrap.md)
- [Cross-repo conventions (metrics, ports)](https://github.com/Cloud-SPE/livepeer-modules-conventions)

Per-repo specifics (capability metric catalog, operator runbooks, exec-plans against this repo's own milestones) land later under `docs/` in this repo.

## License

TBD before first external release.
