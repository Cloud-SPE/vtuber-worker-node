# lint/

Custom lints that enforce architectural invariants beyond what `golangci-lint` handles. Each lint:

- Lives in its own subdirectory.
- Is a Go program invokable via `go run ./lint/<name>/...`.
- Produces structured errors with **remediation instructions** embedded in the message.

## Planned lints

### layer-check

Enforces the dependency rule from `docs/design-docs/architecture.md`:

```
types → config → repo → service → runtime
```

plus `providers/` accessible from all, and `service/modules/<name>/` subject to the same rule internally.

**Status: unimplemented.** Placeholder. Initial enforcement will lean on golangci-lint's `depguard` once 0002-chat-completions-module gives us real import patterns to constrain. Full AST pass opens as a dedicated plan if depguard proves insufficient.

**Tracked:** `tech-debt-tracker.md` entry `layer-check-full-impl`.

### payment-middleware-check

Enforces core belief #3: every paid HTTP route passes through `runtime/http.RegisterPaidRoute`, never `Register` alone. This is the mechanical check that substitutes for "remember to validate payment" discipline.

**Rule:** any call `<x>.Register(method, path, handler)` — three arguments, method name exactly `Register` — where `path` is a string literal starting with `/v1/` is flagged. `/v1/…` is the capability-route namespace per `docs/product-specs/index.md`; such paths MUST be reached via `RegisterPaidRoute`.

**Limitations:**

- Non-literal paths (stored in a variable or const) are NOT flagged. If this becomes a hole, track it and extend the checker to resolve const expressions.
- 2-arg `Register` forms (e.g. `net/http.ServeMux.Handle(pattern, handler)`) are skipped by signature — the lint only fires on 3-arg calls.
- The `lint/` directory is skipped so the linter's own test fixtures don't re-feed themselves.

**Status: delivered.** Runs in `make lint-custom` and in CI via `.github/workflows/lint.yml`. Self-tests at `lint/payment-middleware-check/check_test.go` include a regression guard that walks the real repo.

### no-raw-log

Rejects `fmt.Println`, `log.Print*` in favor of slog. Delivered via golangci-lint's `forbidigo` rule — not a custom Go program.

**Status: scheduled** with the first `.golangci.yml` landing.

### no-secrets-in-logs

AST-based analyzer. Walks every non-test `.go` file, finds `slog.*` calls with literal attr keys matching a deny-list (`password`, `passphrase`, `secret`, `apikey`, `keystore`, `mnemonic`, `authtoken`). Ports the library's implementation — same deny-list, same `//nolint:nosecrets` escape hatch.

**Status: unimplemented.** Will port from the library once relevant code exists.

### doc-gardener

Validates frontmatter + internal links across `docs/design-docs/*.md` and `docs/exec-plans/{active,completed}/*.md`. Checks required frontmatter keys, status consistency with directory, resolvable internal links.

**Status: unimplemented.** Will port from the library once meaningfully populated.

## Format

Lint errors must include:

```
<file>:<line>: <rule-id>: <one-line problem>
  Remediation: <one-or-two sentence guidance>
  See: docs/design-docs/<relevant-doc>.md
```

This lets agents fix violations autonomously from the error message.
