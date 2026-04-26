# PRODUCT_SENSE — vtuber-worker-node

## What we're building

Infrastructure for **operators**, not end-users. The payee-side adapter that lets a host running [`session-runner`](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/session-runner) (plus a `livepeer-modules-project/payment-daemon` receiver daemon, plus a `livepeer-modules-project/service-registry-daemon` publisher daemon) sell `livepeer:vtuber-session` capacity on the Livepeer network. The end-user product (Pipeline → vtuber-livepeer-bridge → this worker) is upstream's concern; this repo's audience is the orchestrator-class operator.

## Who runs this

A Livepeer node operator with:
- A GPU host (NVENC + enough VRAM to run the avatar renderer per-session — see resource budget in [`session-runner.md`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/session-runner.md)).
- An Ethereum keystore for ticket redemption (per `livepeer-modules-project/payment-daemon` receiver mode).
- An HTTPS-served `/.well-known/livepeer-registry.json` path (per `livepeer-modules-project/service-registry-daemon` publisher mode).

They run `vtuber-worker-node` co-located with the three sidecars and `session-runner`. The worker is one process; the sidecars and runner are separate processes on the same host. See [`compose.prod.yaml`](compose.prod.yaml) for the production wiring.

## What we're not building

- **Not a SaaS.** No customer-facing API, no billing UI, no chat sources. Those live in [`Pipeline`](https://github.com/Cloud-SPE/livepeer-vtuber-project/tree/main/pipeline-app) (upstream).
- **Not the bridge.** The bridge ([`vtuber-livepeer-bridge`](https://github.com/Cloud-SPE/vtuber-livepeer-bridge), forthcoming sibling) does customer auth, USD ledger, session-bearer minting, and routes to *some* worker. This repo is the worker side of that pair.
- **Not the inference engine.** Avatar render, LLM calls, TTS — all in `session-runner` (Python). This repo's job is payment middleware + capability routing + module lifecycle.
- **Not chain-aware.** All wei-side logic lives in the `livepeer-modules-project/payment-daemon` receiver daemon. This repo speaks gRPC to it over a unix socket and never touches ETH directly.

## What success looks like

An operator runs `docker compose -f compose.prod.yaml up`, edits `worker.yaml` to set their eth-address + session-runner URL, runs the manifest-publish script (per [`service-registry-publisher-deployment.md`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/exec-plans/active/service-registry-publisher-deployment.md), upstream), and the worker is reachable via `vtuber-livepeer-bridge`'s service-registry resolver. Sessions flow in, get debited every 5s, and balance + grace + close behave per [ADR-006](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/006-streaming-session-payment-pattern.md). No code in this repo handles the end-customer experience.

## Hard constraints

- **No customer credentials in this process.** Per [ADR-005](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/005-bridge-issued-session-bearer.md): the bridge mints a session-scoped child bearer; this worker forwards it to `session-runner`; neither sees the customer's primary key.
- **No YouTube credentials.** Per [ADR-007](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/design-docs/decisions/007-egress-flow-pipeline-bearer.md): `session-runner` pushes media to a Pipeline-issued egress URL; the worker never sees RTMP creds.
- **One eth-address per operator.** A single keystore loaded by the receiver daemon at boot. Multi-eth-address operation is out of scope; operators run multiple workers if they need that.
- **Wire compatibility with `livepeer-modules-project/payment-daemon`.** This repo pins a tag; payments are byte-compatible with `net.Payment`. Drift is a release-blocking bug.
