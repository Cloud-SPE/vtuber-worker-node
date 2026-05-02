---
id: 0002
slug: pattern-b-payment-daemon-alignment
title: Pattern B payment-daemon alignment and worker contract repair
status: completed
owner: agent
opened: 2026-05-02
depends-on: livepeer-vtuber-project Pattern B doc-alignment plan
last-update: 2026-05-03
---

## Goal

Bring `vtuber-worker-node` into conformance with the current Pattern B
streaming-workload contract and the current `livepeer-modules-project`
receiver-side payment-daemon interfaces, without changing the intended
worker-owned runtime debit loop. After this plan, the worker should
consume payment-daemon as a contract rather than a source-library
dependency, expose a coherent worker HTTP/payment surface, and preserve
worker-local debit/runway enforcement for vtuber sessions.

## Non-goals

- No redesign of vtuber into a gateway-ledger-per-tick flow.
- No upstream API changes in `livepeer-modules-project`.
- No changes to customer-facing bridge semantics beyond what the docs
  plan defines as the shared contract.
- No repo-external documentation sweep; that belongs to
  `livepeer-vtuber-project`.

## Approach

- [x] Replace the removed `payment-daemon/config/sharedyaml` dependency
      with a worker-owned config parser/projection modeled on the newer
      worker repos.
- [x] Align worker config and internal catalog terminology on
      `offerings`, removing remaining stale `models` assumptions in
      config parsing, tests, and verification code.
- [x] Update the payee-daemon client/domain projections to the current
      payee proto shape:
      - `offerings`
      - `offering_prices`
      - no `protocol_version`
- [x] Repair startup catalog verification to compare against the current
      daemon catalog shape and the worker’s own parsed offering catalog.
- [x] Define and implement the canonical worker HTTP surface for the
      vtuber workload per the docs-first plan:
      - capability advertisement surface
      - quote/ticket-params helper surface
      - session-open and session-topup routes
- [x] Remove the stale `/capabilities` / `/quote` / `/quotes` surface in
      favor of the docs-first canonical worker advertisement and
      ticket-params routes.
- [x] Preserve and harden the Pattern B runtime loop:
      - `ProcessPayment` on open/topup
      - local `DebitBalance` cadence
      - local `SufficientBalance` runway checks
      - `CloseSession` on termination
- [x] Pin and document the session-correlation contract in code:
      - `gateway_session_id`
      - `worker_session_id`
      - `work_id`
      - `usage_seq`
- [x] Add the worker-side topup path so additional payment blobs can be
      applied to the same live `work_id`.
- [x] Update tests to cover:
      - config parsing
      - payee client projection
      - startup verification
      - open/topup/close lifecycle
      - usage tick and low-balance event behavior
- [x] Align the repo’s Go/toolchain requirements with the current
      sibling dependency floor or otherwise decouple local replaces
      enough that the worker builds cleanly in-repo.

## Decisions log

### 2026-05-02 — Worker keeps Pattern B local enforcement

Reason: The target suite pattern for streaming workloads is Pattern B:
the worker and co-located receiver-side daemon remain the
runtime-critical allowance meter. This plan therefore adapts the worker
to the current payment-daemon contract while preserving worker-local
debit and runway control.

### 2026-05-02 — Source-library coupling to payment-daemon is removed

Reason: The current sibling worker examples treat payment-daemon as a
runtime contract and proto/config boundary, not as a source package to
import for internal parser types. The removed `config/sharedyaml`
package is concrete evidence that this repo must own its own projection.

## Open questions

- None for this alignment pass.

## Artifacts produced

- Repo-local worker exec plan only. Follow-on PRs should link here.

## Progress log

- 2026-05-02 — Replaced the removed `sharedyaml` import path with a
  worker-owned YAML parser and validator, including `work_unit: second`
  support and offering-based route projection.
- 2026-05-02 — Updated the local payee-daemon client to the current
  proto shape (`offerings`, `offering_prices`, no `protocol_version`)
  and added the exact `GetTicketParams` proxy surface used by the newer
  worker pattern.
- 2026-05-02 — Removed the stale `/capabilities`, `/quote`, and
  `/quotes` surface from the mounted worker API; canonical unpaid
  routes are now `/registry/offerings` and
  `POST /v1/payment/ticket-params`.
- 2026-05-02 — Added worker-side session correlation for Pattern B
  topups (`gateway_session_id -> worker_session_id -> work_id`) and
  propagated `worker_session_id`, `work_id`, and `usage_seq` in
  worker-originated vtuber usage events.
- 2026-05-03 — Added the canonical
  `POST /api/sessions/{gateway_session_id}/end` route. The worker now
  supports open, topup, and graceful end on the final bridge contract.
