# syntax=docker/dockerfile:1.7

# --- builder ---
# CGO_ENABLED=0 for a statically-linked binary that drops into
# distroless/static. `netgo,osusergo` guarantee no glibc shim deps.
#
# This Dockerfile assumes two build contexts:
#   - the default context = this repo (vtuber-worker-node)
#   - a named context `library` = livepeer-payment-library, made
#     available via compose `additional_contexts` (see compose.yaml)
#     OR via buildx `--build-context library=../livepeer-payment-library`.
#
# Once the library tags a release and vtuber-worker-node drops the
# `replace` directive in go.mod, the `library` context goes away and
# this file becomes a standard single-context Go build.
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Sibling library into a path the `replace` directive can reach. The
# sed rewrite points the replace at the in-container path, preserving
# the local-dev workflow without losing the go.mod-honesty of the
# `replace` directive in source.
COPY --from=library . /sibling/livepeer-payment-library

COPY go.mod go.sum ./
# rewrite-then-download primes the module cache before the full source
# copy so we keep the layer cache hot when only Go source changes.
RUN sed -i \
        's|replace github.com/Cloud-SPE/livepeer-payment-library => ../livepeer-payment-library|replace github.com/Cloud-SPE/livepeer-payment-library => /sibling/livepeer-payment-library|' \
        go.mod && \
    go mod download

COPY . .

# COPY . . above re-overlaid the original go.mod from the build context.
# Re-apply the sed so `go build` sees the in-container replace path.
RUN sed -i \
        's|replace github.com/Cloud-SPE/livepeer-payment-library => ../livepeer-payment-library|replace github.com/Cloud-SPE/livepeer-payment-library => /sibling/livepeer-payment-library|' \
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
