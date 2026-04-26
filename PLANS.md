# PLANS

Index of execution plans against this repo. Plans are first-class artifacts checked into the repo with progress + decision logs.

## Active

| Plan | Purpose |
|---|---|
| (no in-repo plans yet) | The current build sequence is governed by [`vtuber-worker-node-bootstrap.md`](https://github.com/Cloud-SPE/livepeer-vtuber-project/blob/main/docs/exec-plans/active/vtuber-worker-node-bootstrap.md) in the upstream `livepeer-vtuber-project`. As the repo accrues per-repo work (refactors, perf items, repo-local milestones), they land here as `docs/exec-plans/active/NNNN-<name>.md`. |

## Completed

| Plan | Completed | Outcome |
|---|---|---|
| (none yet) | | |

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
