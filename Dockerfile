# syntax=docker/dockerfile:1.7

# --- builder ---
# CGO_ENABLED=0 for a statically-linked binary that drops into
# distroless/static. `netgo,osusergo` guarantee no glibc shim deps.
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

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
