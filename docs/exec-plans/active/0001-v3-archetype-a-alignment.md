---
id: 0001
slug: v3-archetype-a-alignment
title: v3.0.0 archetype-A alignment (drop publisherdaemon, offerings rename, /registry/offerings)
status: active
owner: agent
opened: 2026-04-29
depends-on: livepeer-network-suite plan 0003 §E
---

## Goal

Strip archetype-B (worker self-publishing) from this worker, rename
`models:` → `offerings:` in the worker.yaml schema and config parser to
match modules v3.0.0, and add a `/registry/offerings` HTTP endpoint
exposing the modules-canonical capability fragment so the
orch-coordinator can scrape this worker into a draft roster row.

## Non-goals

- No backwards-compat: `service_registry_publisher` blocks and old
  `models:` syntax in worker.yaml are parse-time errors, not warnings.
- No payment-daemon protocol changes.
- No coordinator-side scrape implementation (lives in
  livepeer-orch-coordinator).

## Approach

- [ ] Delete `internal/providers/publisherdaemon/` (the gRPC client to
      the publisher daemon).
- [ ] Delete the conditional wiring in
      `cmd/vtuber-worker-node/main.go` (around lines 165–175 and 360)
      that loads + invokes the publisher when
      `worker.service_registry_publisher` is set.
- [ ] Strip the `service_registry_publisher` block (lines 67–92)
      entirely from `worker.example.yaml`.
- [ ] Worker.yaml schema: rely on the existing `yaml.KnownFields(true)`
      strict-parse — no special-case "deprecated" branch. The
      `service_registry_publisher` key simply isn't recognised.
- [ ] Rename `models:` → `offerings:` in the `capabilities[]` block of
      `worker.example.yaml` and update the worker config parser
      (likely under `internal/config/`) to match modules v3.0.0.
- [ ] Implement `/registry/offerings` HTTP endpoint in
      `internal/runtime/http/` (or equivalent) emitting the
      modules-canonical fragment built from the worker's
      `capabilities[]` config:
      - `name`, `work_unit`, `offerings[].id`,
        `offerings[].price_per_work_unit_wei`
      - `extra` carrying the streaming-session knobs
        (`debit_cadence_seconds`, `sufficient_min_runway_seconds`,
        `sufficient_grace_seconds`).
- [ ] Optional bearer auth via a new `OFFERINGS_AUTH_TOKEN` env. If
      set, the endpoint requires `Authorization: Bearer <token>`;
      otherwise plain HTTP. Default off.
- [ ] Update `DESIGN.md` and `README.md`: archetype-A framing —
      "worker is registry-invisible; orch-coordinator scrapes
      `/registry/offerings` and the operator confirms".
- [ ] Tag `v3.0.0` (currently pinned at `52a0336`, no tag).

## Decisions log

## Open questions

- **Modules-project version tag** — assume `v3.0.0`; confirm before
  matching the worker.yaml schema rename.
- **Manifest `schema_version` integer** — CONFIRMED `3` (operator answered 2026-04-29); not directly
  consumed here but referenced in `/registry/offerings` design notes.
- **Daemon image pinning** — CONFIRMED `compose.prod.yaml` pins
  `payment-daemon` (and any sibling daemons) to `v3.0.0` hardcoded.
  No tech-debt entry needed — every component lands at v3.0.0 in this
  wave.
- Should `/registry/offerings` be served on the same HTTP listener as
  the workload routes, or on the metrics listener (port-shared with
  `/metrics`)?

## Artifacts produced
