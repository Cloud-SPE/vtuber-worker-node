# Running With Docker

Production bring-up for one vtuber worker host.

This stack is three local services:

- `payment-daemon` `v4.0.1` or newer in receiver mode
- `session-runner` as the local backend
- `vtuber-worker-node` as the public worker surface

The worker is not standalone. It requires:

- a local unix-socket connection to `payment-daemon`
- a local HTTP connection to `session-runner`
- a real `worker.yaml`
- a real recipient keystore

## First-production posture

Start simple:

- one host
- one `session-runner`
- one `vtuber-worker-node`
- one `payment-daemon`
- `worker.max_concurrent_requests: 1`
- static registration in your orchestrator / bridge at first

Do not add service-registry publisher automation in the first pass from this repo. The current worker parser rejects the old `service_registry_publisher` block, and the safe first production posture is a statically registered worker.

## Prerequisites

You need:

- Docker Engine + Docker Compose plugin
- a GPU-capable host suitable for Chromium rendering
- an Arbitrum RPC URL with write access for ticket redemption
- a recipient keystore JSON and a separate password file
- a vtuber worker image
- a session-runner image

The worker backend URL in `worker.yaml` must point at the local runner:

```yaml
backend_url: "http://session-runner:8080/api/sessions/start"
```

## Files

Prepare these files in one directory on the host:

1. `worker.yaml`
2. `.env`
3. `keystore.json`
4. `keystore-password`

Use:

- [worker.example.yaml](../../../worker.example.yaml)
- [.env.example](../../../.env.example)
- [compose.prod.yaml](../../../compose.prod.yaml)

## worker.yaml checklist

Start from `worker.example.yaml` and set:

- `payment_daemon.recipient_eth_address`
- `payment_daemon.broker.mode: ethereum`
- `payment_daemon.broker.rpc_url`
- `worker.http_listen`
- `worker.payment_daemon_socket`
- `worker.max_concurrent_requests: 1`
- `capabilities[].offerings[].backend_url: http://session-runner:8080/api/sessions/start`

With the current payment contract, the worker opens the payee-side session before first credit, the first successful `ProcessPayment` binds sender, and later topups/debits run against that same bound session.

For the first production host:

- keep one offering only, if you want the smallest blast radius
- keep `verify_daemon_consistency_on_start: true`
- remove any `service_registry_publisher` block if you copied an older file

## .env checklist

At minimum set:

- `VTUBER_WORKER_IMAGE`
- `SESSION_RUNNER_IMAGE`
- `CHAIN_RPC`
- `RECIPIENT_KEYSTORE_PATH`
- `RECIPIENT_KEYSTORE_PASSWORD_PATH`
- `WORKER_YAML_PATH`

Recommended first-pass runner settings:

```dotenv
SESSION_RUNNER_RENDERER=chromium
SESSION_RUNNER_LLM=mock
SESSION_RUNNER_TTS=mock
```

That keeps the first production deployment focused on the core worker/payment/session lifecycle. Move to `livepeer` LLM/TTS only after the host is proven healthy.

## Bring it up

```bash
docker network create cf-tunnel || true
docker compose -f compose.prod.yaml --env-file .env up -d
```

## Validate locally

Check the containers:

```bash
docker compose -f compose.prod.yaml --env-file .env ps
```

Check the worker:

```bash
curl -fsS http://127.0.0.1:${WORKER_HOST_PORT:-8080}/health
curl -fsS http://127.0.0.1:${WORKER_HOST_PORT:-8080}/registry/offerings
```

Check the runner from inside the docker network:

```bash
docker compose -f compose.prod.yaml --env-file .env exec -T session-runner \
  python - <<'PY'
import urllib.request
print(urllib.request.urlopen("http://127.0.0.1:8080/api/health").read().decode())
PY
```

Check the worker logs:

```bash
docker compose -f compose.prod.yaml --env-file .env logs -f vtuber-worker
```

Check the runner logs:

```bash
docker compose -f compose.prod.yaml --env-file .env logs -f session-runner
```

## Ingress

Expose only the worker externally.

- `vtuber-worker-node` is the public HTTP surface
- `session-runner` stays private on the docker network
- `payment-daemon` stays private on the docker network

If Cloudflare Tunnel runs as a sibling container on the `cf-tunnel` network, point it at:

```text
http://vtuber-worker:8080
```

If Cloudflare Tunnel runs on the host, point it at:

```text
http://127.0.0.1:${WORKER_HOST_PORT}
```

## What this does not do yet

- service-registry publisher in this stack
- worker-side backend pooling
- multi-runner capacity routing

Those can come later. For the first deploy, the correct target is one healthy worker host that can accept one session cleanly.
