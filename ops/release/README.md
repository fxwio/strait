# v1.0 Internal Trial Release Runbook

This project is now in **release closure** for the internal-trial `v1.0`.
Do not keep optimizing after the release gates pass. Freeze the code.

## Release Gates

The release candidate is allowed to ship only when all of the following have evidence:

1. `go test ./...` passes
2. `go test -tags=integration ./integration/...` passes
3. Gateway self-test has real reports for:
   - steady load: `500 QPS`
   - peak target: `5000 QPS`
   - `10` mock upstream providers
   - `256 KiB` average request bodies
   - `1 MiB` average upstream responses
   - `5000` peak concurrency
   - `1m` duration
4. Real provider smoke passes
5. `/metrics` is protected
6. Docker Compose deployment uses an explicit image tag
7. A rollback tag is prepared before deployment
8. Trial scope is limited to a pilot token or a single environment

## Self-Test

Build and run the gateway self-test:

```bash
make release-selftest
```

Reports are written under `ops/release/reports/`.

For a shorter local dry-run before the 1-minute acceptance:

```bash
SELFTEST_DURATION=20s SELFTEST_NORMAL_QPS=50 SELFTEST_PEAK_QPS=200 make release-selftest
```

Interpretation:

- `panic_count` must be `0`
- `error_rate` must be `<= 0.003`
- `timeout_rate` must be `<= 0.005`
- `added_latency.p50_ms` must be `<= 15`
- `added_latency.p95_ms` must be `<= 50`
- `added_latency.p99_ms` must be `<= 100`
- `stream_added_first_byte.p99_ms` must be `<= 100`
- `streaming_buffered` must not exceed `0.1%` of stream traffic
- the last metrics window must not show continuous growth in goroutines or RSS

If the target machine cannot sustain `5000 QPS`, record the highest sustained passing result and use that number as the internal-trial capacity claim. Do not invent the number.

## Real Provider Smoke

Run the real-provider smoke against actual provider keys already present in the environment.
For your current release path, prefer SiliconFlow:

```bash
REAL_SMOKE_PROVIDER=siliconflow make release-real-smoke
```

The script verifies:

- `/health/live`
- `/metrics` auth boundary
- unauthenticated request rejection
- real provider non-stream request
- real provider streaming request
- forced timeout returns `504`
- forced upstream auth failure stays bounded

Supported real providers in the smoke script:

- `siliconflow` via `SILICONFLOW_API_KEY`
- `openai` via `OPENAI_API_KEY`
- `anthropic` via `ANTHROPIC_API_KEY`

## Minimal Trial Boundary

The default internal-trial policy is:

1. one dedicated pilot token only
2. low per-token QPS / burst
3. single instance or single environment first

Use the pilot token in `auth.tokens` and keep its `rate_limit_qps`, `rate_limit_burst`, and optional `allowed_models` narrow.

Immediate stop-loss:

```bash
docker compose down
```

If the gateway binary is healthy but the new image is bad, roll back by switching the image tag and bringing the service back up.

## Image Tagging And Rollback

Build the release image:

```bash
make image IMAGE_REPO=strait IMAGE_TAG=v1.0.0-rc1
```

Deploy the tagged image:

```bash
make deploy-image IMAGE_REPO=strait IMAGE_TAG=v1.0.0-rc1
```

Roll back to the previous stable tag:

```bash
make deploy-image IMAGE_REPO=strait IMAGE_TAG=v0.9.3
```

After rollback, verify:

1. `/health/live` returns `200`
2. `/health/ready` returns `200` or expected degraded state
3. unauthenticated chat returns `401`
4. the pilot token succeeds
5. `/metrics` remains protected

## Code Freeze

When all gates pass:

1. stop performance work
2. stop refactoring
3. only allow release-note or emergency patch changes
4. tag the image
5. announce **code freeze**
