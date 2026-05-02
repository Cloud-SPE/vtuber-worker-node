# PLANS

Index of execution plans against this repo. Plans are first-class artifacts checked into the repo with progress + decision logs.

## Active

| Plan | Purpose |
|---|---|
| [0001-v3-archetype-a-alignment](docs/exec-plans/active/0001-v3-archetype-a-alignment.md) | Earlier repo-local v3 alignment draft. Retained as active history until explicitly completed or superseded. |

## Completed

| Plan | Completed | Outcome |
|---|---|---|
| [0002-pattern-b-payment-daemon-alignment](docs/exec-plans/active/0002-pattern-b-payment-daemon-alignment.md) | 2026-05-03 | Worker aligned to the current Pattern B vtuber contract and current receiver-side payment-daemon interfaces, including canonical open/topup/end routes. |

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
