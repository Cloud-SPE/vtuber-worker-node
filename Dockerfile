# syntax=docker/dockerfile:1.7

# --- builder ---
# CGO_ENABLED=0 for a statically-linked binary that drops into
# distroless/static. `netgo,osusergo` guarantee no glibc shim deps.
#
# This Dockerfile assumes three build contexts:
#   - the default context = this repo (vtuber-worker-node)
#   - a named context `library`  = livepeer-payment-library
#   - a named context `registry` = livepeer-service-registry
#
# Both siblings are reached via `additional_contexts` from compose
# (see compose.yaml) or via buildx flags:
#   --build-context library=../livepeer-payment-library
#   --build-context registry=../livepeer-service-registry
#
# Once both libraries tag releases and vtuber-worker-node drops the
# `replace` directives in go.mod, the additional contexts go away and
# this file becomes a standard single-context Go build.
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Siblings into paths the `replace` directives can reach. The sed
# rewrite below points each replace at the in-container path,
# preserving the local-dev workflow without losing the go.mod-honesty
# of the `replace` directives in source.
COPY --from=library  . /sibling/livepeer-payment-library
COPY --from=registry . /sibling/livepeer-service-registry

COPY go.mod go.sum ./
# rewrite-then-download primes the module cache before the full source
# copy so we keep the layer cache hot when only Go source changes.
RUN sed -i \
        -e 's|replace github.com/Cloud-SPE/livepeer-payment-library => ../livepeer-payment-library|replace github.com/Cloud-SPE/livepeer-payment-library => /sibling/livepeer-payment-library|' \
        -e 's|replace github.com/Cloud-SPE/livepeer-service-registry => ../livepeer-service-registry|replace github.com/Cloud-SPE/livepeer-service-registry => /sibling/livepeer-service-registry|' \
        go.mod && \
    go mod download

COPY . .

# COPY . . above re-overlaid the original go.mod from the build context.
# Re-apply the sed so `go build` sees the in-container replace paths.
RUN sed -i \
        -e 's|replace github.com/Cloud-SPE/livepeer-payment-library => ../livepeer-payment-library|replace github.com/Cloud-SPE/livepeer-payment-library => /sibling/livepeer-payment-library|' \
        -e 's|replace github.com/Cloud-SPE/livepeer-service-registry => ../livepeer-service-registry|replace github.com/Cloud-SPE/livepeer-service-registry => /sibling/livepeer-service-registry|' \
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
