# PLANS

Index of execution plans against this repo. Plans are first-class artifacts checked into the repo with progress + decision logs.

## Active

| Plan | Purpose |
|---|---|
| None | — |

## Completed

| Plan | Completed | Outcome |
|---|---|---|
| [0002-pattern-b-payment-daemon-alignment](docs/exec-plans/active/0002-pattern-b-payment-daemon-alignment.md) | 2026-05-03 | Worker aligned to the current Pattern B vtuber contract and current receiver-side payment-daemon interfaces, including canonical open/topup/end routes. |
| [0003-standalone-ci-and-release-alignment](docs/exec-plans/active/0003-standalone-ci-and-release-alignment.md) | 2026-05-02 | Standalone PR/main CI and Docker Hub tag-driven release lanes aligned to the OpenAI-style repo boundary. |

## Superseded

| Plan | Superseded | By |
|---|---|---|
| [0001-v3-archetype-a-alignment](docs/exec-plans/active/0001-v3-archetype-a-alignment.md) | 2026-05-02 | [0002-pattern-b-payment-daemon-alignment](docs/exec-plans/active/0002-pattern-b-payment-daemon-alignment.md) |

## How plans work

Same convention as siblings (`openai-worker-node`, `livepeer-modules-project/payment-daemon`):

```markdown
---
status: active | completed | blocked
owner: <agent name or human>
created: YYYY-MM-DD
last-update: YYYY-MM-DD
---
```

Each plan has Goal / Acceptance criteria / Non-goals / Approach / Progress log / Decisions / Open questions. Small changes don't need a plan file — a well-scoped PR suffices. Anything that spans more than one PR, one day, or one reviewer gets a plan.
