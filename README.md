# Strait

Minimal OpenAI-compatible AI Gateway written in Go.

`Strait` only covers gateway core:
- OpenAI-compatible `/v1/chat/completions`
- token auth
- basic rate limiting
- provider/model routing
- request forwarding
- SSE streaming proxy
- health, metrics, logging
- graceful shutdown

Not in scope:
- plugin systems
- admin platforms
- databases, caches, queues
- multi-tenant control planes
- heavy middleware frameworks

## v1.0 Status

This repository is in `v1.0` release closure for **internal trial**.

Validated release boundary on the current test machine:
- `500 QPS / 1m`: pass
- `1000 QPS / 1m`: pass
- `1500 QPS+`: fail

Internal-trial release position:
- single instance or single environment
- pilot token only
- low per-token QPS / burst
- explicit image tag deployment
- rollback tag prepared before release

## Quick Start

Prepare local files:

```bash
cp .env.example .env
cp config.example.yaml config.yaml
```

Set at least:

```bash
export GATEWAY_TOKEN_DEFAULT=sk-gateway-your-secret-here
export SILICONFLOW_API_KEY=your-siliconflow-key
```

Run locally:

```bash
make run
```

Gateway listens on `http://localhost:8080`.

## Minimal Config

```yaml
server:
  port: 8080
  shutdown_timeout: 300s

auth:
  rate_limit_qps: 10
  rate_limit_burst: 20
  tokens:
    - name: "pilot"
      value_env: "GATEWAY_TOKEN_DEFAULT"
      rate_limit_qps: 5
      rate_limit_burst: 10

providers:
  - name: "siliconflow"
    base_url: "https://api.siliconflow.cn"
    api_key_env: "SILICONFLOW_API_KEY"
    models: ["Pro/MiniMaxAI/MiniMax-M2.5"]
```

For OpenAI-compatible providers such as SiliconFlow, `base_url` must be the provider host only.  
Do **not** set it to the full `/v1/chat/completions` endpoint. The gateway appends that path itself.

## Request Example

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_TOKEN_DEFAULT" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Pro/MiniMaxAI/MiniMax-M2.5",
    "messages": [
      {"role": "user", "content": "hello"}
    ],
    "stream": false
  }'
```

## Health And Metrics

- liveness: `GET /health/live`
- readiness: `GET /health/ready`
- metrics: `GET /metrics`

If `GATEWAY_METRICS_TOKEN` is set, `/metrics` requires:

```bash
curl http://localhost:8080/metrics \
  -H "Authorization: Bearer $GATEWAY_METRICS_TOKEN"
```

## Docker Deployment

Build release image:

```bash
make image IMAGE_REPO=strait IMAGE_TAG=v1.0.0-rc1
```

Deploy tagged image:

```bash
make deploy-image IMAGE_REPO=strait IMAGE_TAG=v1.0.0-rc1
```

`deploy-image` intentionally rejects `IMAGE_TAG=local`.

## Release Validation

Run gateway self-test:

```bash
make release-selftest
```

Run real provider smoke:

```bash
REAL_SMOKE_PROVIDER=siliconflow make release-real-smoke
```

Detailed release procedure is in [ops/release/README.md](ops/release/README.md).

## Rollback

Immediate stop:

```bash
docker compose down
```

Roll back to the previous stable image:

```bash
make deploy-image IMAGE_REPO=strait IMAGE_TAG=<previous-stable-tag>
```

Verify after rollback:
- `/health/live` returns `200`
- `/health/ready` returns `200` or expected degraded state
- unauthenticated chat returns `401`
- pilot token request succeeds
- `/metrics` remains protected

## License

MIT
