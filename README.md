# vtuber-worker-node

Payee-side HTTP adapter for the **`livepeer:vtuber-session`** capability. Hosts the [`StreamingModule`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/streaming-session-module.md) interface, runs the payment middleware via a co-located [`livepeer-modules-project/payment-daemon`](https://github.com/Cloud-SPE/livepeer-modules-project/tree/main/payment-daemon) receiver daemon, publishes its capability via a co-located [`livepeer-modules-project/service-registry-daemon`](https://github.com/Cloud-SPE/livepeer-modules-project/tree/main/service-registry-daemon) publisher daemon, and forwards session-open requests to its local [`session-runner`](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/session-runner) backend.

Sibling repo of [`openai-worker-node`](https://github.com/Cloud-SPE/openai-worker-node); structurally shaped after it. Per [ADR-003 in livepeer-vtuber-project](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/003-sibling-project-integration.md), the two repos share no source code — common patterns are documented in the [shared-patterns table](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/sibling-integration.md).

## Status

Implements the `livepeer:vtuber-session` worker contract end-to-end:

- payment-daemon receiver integration
- local proto-owned payment-daemon wire contract; no upstream Go-module import
- explicit `OpenSession` before first credit, with first successful `ProcessPayment` binding sender
- retry-stable `DebitBalance(sender, work_id, work_units, debit_seq)`
- `offerings` advertisement and ticket-params surface
- worker open/topup/end session routes
- local `session-runner` backend forwarding

The next major track is worker-side backend pooling and capacity routing. See the upstream design and execution plans in `livepeer-vtuber-project` for the broader system roadmap.

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
proto/                     # local payment-daemon wire contract snapshot
```

## Build + test

```bash
go build -o bin/vtuber-worker-node ./cmd/vtuber-worker-node
go test ./...
make lint                # golangci-lint + custom payment-middleware-check
```

## Production deployment

Start with [compose.prod.yaml](compose.prod.yaml), [.env.example](.env.example), and the operator runbook at [docs/operations/running-with-docker.md](docs/operations/running-with-docker.md). The minimum production host is:

- `payment-daemon` (receiver mode)
- `session-runner`
- `vtuber-worker-node`

For the first deployment, prefer one host, one runner, one worker, and static registration in your orchestrator rather than resolver/publisher automation.

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
