---
status: active
owner: Codex
created: 2026-05-02
last-update: 2026-05-02
---

# 0003 — standalone ci and release alignment

## Goal

Make `vtuber-worker-node` fully standalone in CI and release handling: no sibling checkout assumptions, clear required PR checks, and a tag-driven image release path that matches the intended OpenAI-style repo boundary.

## Acceptance criteria

1. All required PR checks run in a fresh checkout with no sibling repos present.
2. Required PR checks are limited to repo-local correctness:
   - docs check if applicable
   - `go test ./...`
   - `make lint`
   - Docker build smoke
3. Release automation exists for semver tags and publishes a worker image tagged with both semver and commit SHA.
4. Image scan policy is explicit and runs in the release lane.
5. Branch protection can require only repo-local deterministic jobs.

## Non-goals

- Full-stack vtuber integration in this repo's required PR lane.
- Reintroducing any local checkout dependency on `livepeer-modules-project`.
- Changing worker runtime behavior beyond what CI/release setup requires.

## Approach

- [ ] Audit current `.github/workflows/` against the target matrix.
- [ ] Separate required PR checks from release-only jobs.
- [ ] Add or normalize a Docker build smoke job for PRs.
- [ ] Add tag-driven image build/push workflow.
- [ ] Add image scan step in the release lane.
- [ ] Document the release contract in `README.md` or a repo-local release note.
- [ ] Update this plan with the final protected-check list.

## Progress log

### 2026-05-02

Plan opened after the Pattern B/payment-daemon alignment release. Repo is already standalone for code/test/lint and no longer requires sibling checkouts; remaining work is workflow normalization and artifact publishing.

## Decisions

### 2026-05-02 — release and integration are separate lanes

Reason: the worker repo should prove its own correctness on every PR, but published-image vtuber integration belongs in a heavier release or integration lane owned outside this repo's required checks.

## Open questions

- Which container registry is the canonical publish target for worker images?
- Should release publish also attach SBOM/provenance, or should that wait until all vtuber repos share one release template?

