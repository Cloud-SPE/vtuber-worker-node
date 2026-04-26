.PHONY: build test lint lint-custom doc-lint docker-build docker-build-version clean

build:
	go build -o bin/vtuber-worker-node ./cmd/vtuber-worker-node

test:
	go test -race ./...

lint:
	golangci-lint run ./...
	$(MAKE) lint-custom

# Custom lints that enforce invariants beyond what golangci-lint expresses.
# See lint/README.md.
lint-custom:
	go run ./lint/payment-middleware-check --root .

doc-lint:
	@echo "doc-lint: placeholder until doc-gardener is wired in"

# Build the container image as tztcloud/livepeer-vtuber-worker-node:dev.
# Override tag via DOCKER_TAG=... to publish-name it (e.g.
# `make docker-build DOCKER_TAG=v0.8.10`).
#
# `--build-context library=...` feeds the sibling livepeer-payment-library
# repo into the Dockerfile's named build context. Required while go.mod
# carries the local `replace` directive; goes away once the library
# tags a release.
DOCKER_TAG ?= dev
DOCKER_IMAGE ?= tztcloud/livepeer-vtuber-worker-node
# Note: do NOT call this LIBRARY_PATH — that name collides with a
# common env var (CUDA toolchains export it) and the override would
# silently retarget the build context.
LIBRARY_CONTEXT ?= ../livepeer-payment-library

docker-build:
	docker build \
		--build-arg VERSION=$(DOCKER_TAG) \
		--build-context library=$(LIBRARY_CONTEXT) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-push:
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

docker-build-version:
	$(MAKE) docker-build DOCKER_TAG=$$(git rev-parse --short HEAD)

clean:
	rm -rf bin/
