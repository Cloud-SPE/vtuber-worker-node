# syntax=docker/dockerfile:1.7

# --- builder ---
# CGO_ENABLED=0 for a statically-linked binary that drops into
# distroless/static. `netgo,osusergo` guarantee no glibc shim deps.
#
# This Dockerfile assumes four build contexts:
#   - the default context = this repo (vtuber-worker-node)
#   - a named context `library`       = livepeer-modules-project/payment-daemon
#   - a named context `registry`      = livepeer-modules-project/service-registry-daemon
#   - a named context `chain_commons` = livepeer-modules-project/chain-commons
#
# All three are reached via `additional_contexts` from compose
# (see compose.yaml) or via buildx flags:
#   --build-context library=../livepeer-modules-project/payment-daemon
#   --build-context registry=../livepeer-modules-project/service-registry-daemon
#   --build-context chain_commons=../livepeer-modules-project/chain-commons
#
# `chain_commons` is required because both payment-daemon and
# service-registry-daemon transitively replace
# `github.com/Cloud-SPE/livepeer-modules-project/chain-commons => ../chain-commons`
# in their own go.mod files. Go's `replace` directives don't propagate
# from libraries, so we replicate that replace at the worker's go.mod
# level — and the build needs chain-commons in the build context to
# satisfy it.
#
# Once livepeer-modules-project's plan 0008 publishes tagged Go modules,
# all three replaces drop out and this becomes a standard single-context
# Go build.
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Siblings into paths the `replace` directives can reach. The sed
# rewrites below point each replace at the in-container path,
# preserving the local-dev workflow without losing the go.mod-honesty
# of the `replace` directives in source.
COPY --from=library       . /sibling/payment-daemon
COPY --from=registry      . /sibling/service-registry-daemon
COPY --from=chain_commons . /sibling/chain-commons

# The two daemons' OWN go.mod files reference chain-commons via the
# relative path `../chain-commons`. After we copy them into /sibling/,
# that relative path resolves to /sibling/chain-commons — which is
# exactly where we put it. So the daemon-side go.mod replaces don't
# need any rewriting.

COPY go.mod go.sum ./
# rewrite-then-download primes the module cache before the full source
# copy so we keep the layer cache hot when only Go source changes.
RUN sed -i \
        -e 's|replace github.com/Cloud-SPE/livepeer-modules-project/payment-daemon => ../livepeer-modules-project/payment-daemon|replace github.com/Cloud-SPE/livepeer-modules-project/payment-daemon => /sibling/payment-daemon|' \
        -e 's|replace github.com/Cloud-SPE/livepeer-modules-project/service-registry-daemon => ../livepeer-modules-project/service-registry-daemon|replace github.com/Cloud-SPE/livepeer-modules-project/service-registry-daemon => /sibling/service-registry-daemon|' \
        -e 's|replace github.com/Cloud-SPE/livepeer-modules-project/chain-commons => ../livepeer-modules-project/chain-commons|replace github.com/Cloud-SPE/livepeer-modules-project/chain-commons => /sibling/chain-commons|' \
        go.mod && \
    go mod download

COPY . .

# COPY . . above re-overlaid the original go.mod from the build context.
# Re-apply the sed so `go build` sees the in-container replace paths.
RUN sed -i \
        -e 's|replace github.com/Cloud-SPE/livepeer-modules-project/payment-daemon => ../livepeer-modules-project/payment-daemon|replace github.com/Cloud-SPE/livepeer-modules-project/payment-daemon => /sibling/payment-daemon|' \
        -e 's|replace github.com/Cloud-SPE/livepeer-modules-project/service-registry-daemon => ../livepeer-modules-project/service-registry-daemon|replace github.com/Cloud-SPE/livepeer-modules-project/service-registry-daemon => /sibling/service-registry-daemon|' \
        -e 's|replace github.com/Cloud-SPE/livepeer-modules-project/chain-commons => ../livepeer-modules-project/chain-commons|replace github.com/Cloud-SPE/livepeer-modules-project/chain-commons => /sibling/chain-commons|' \
        go.mod

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -tags 'netgo osusergo' \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/vtuber-worker-node \
        ./cmd/vtuber-worker-node

# --- runtime ---
# distroless/static:nonroot is ~2 MB, has CA bundle + /etc/passwd, runs
# as uid/gid 65532:65532. No shell; exec won't help.
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/vtuber-worker-node /usr/local/bin/vtuber-worker-node

USER nonroot:nonroot

# HTTP port documented for operator clarity. The actual bind address
# comes from worker.yaml (worker.http_listen).
EXPOSE 8080

# ENTRYPOINT is the binary; operator supplies `--config` + flags via
# compose command or `docker run` args.
ENTRYPOINT ["/usr/local/bin/vtuber-worker-node"]
