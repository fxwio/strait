#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GOCACHE="${GOCACHE:-/tmp/strait-gocache}"
GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
GOSUMDB="${GOSUMDB:-sum.golang.org}"

PORT="${REAL_SMOKE_PORT:-18080}"
GATEWAY_BIN="${ROOT_DIR}/build/strait"
METRICS_TOKEN="${REAL_SMOKE_METRICS_TOKEN:-release-smoke-metrics-token}"
GATEWAY_TOKEN="${GATEWAY_TOKEN_DEFAULT:-${REAL_SMOKE_GATEWAY_TOKEN:-release-smoke-gateway-token}}"
OPENAI_MODEL="${OPENAI_SMOKE_MODEL:-gpt-4o-mini}"
ANTHROPIC_MODEL="${ANTHROPIC_SMOKE_MODEL:-claude-3-5-sonnet-latest}"
SILICONFLOW_MODEL="${SILICONFLOW_SMOKE_MODEL:-Pro/MiniMaxAI/MiniMax-M2.5}"
REAL_SMOKE_PROVIDER="${REAL_SMOKE_PROVIDER:-}"
REAL_SMOKE_CLEAR_PROXY="${REAL_SMOKE_CLEAR_PROXY:-1}"

HAVE_OPENAI_KEY=0
HAVE_ANTHROPIC_KEY=0
HAVE_SILICONFLOW_KEY=0
if [[ -n "${OPENAI_API_KEY:-}" ]]; then
  HAVE_OPENAI_KEY=1
fi
if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
  HAVE_ANTHROPIC_KEY=1
fi
if [[ -n "${SILICONFLOW_API_KEY:-}" ]]; then
  HAVE_SILICONFLOW_KEY=1
fi

say() {
  printf '%s\n' "$*"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing command: $1"
}

need_cmd curl
need_cmd go

mkdir -p build
if [[ ! -x "$GATEWAY_BIN" || "${REAL_SMOKE_REBUILD:-0}" == "1" ]]; then
  env GOCACHE="$GOCACHE" GOPROXY="$GOPROXY" GOSUMDB="$GOSUMDB" \
    go build -trimpath -ldflags "-s -w" -o "$GATEWAY_BIN" ./cmd/gateway/main.go
fi

WORK_DIR="$(mktemp -d)"
LOG_FILE="${WORK_DIR}/gateway.log"
CONFIG_FILE="${WORK_DIR}/config.yaml"
GW_PID=""

cleanup() {
  if [[ -n "$GW_PID" ]] && kill -0 "$GW_PID" >/dev/null 2>&1; then
    kill -TERM "$GW_PID" >/dev/null 2>&1 || true
    wait "$GW_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

status_code() {
  curl --noproxy '*' -sS -o /dev/null -w "%{http_code}" "$@"
}

wait_live() {
  local target="$1"
  for _ in $(seq 1 60); do
    if [[ "$(status_code "${target}/health/live" || true)" == "200" ]]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

write_config() {
  local timeout_override="$1"

  cat >"$CONFIG_FILE" <<EOF
server:
  host: "127.0.0.1"
  port: ${PORT}
  read_timeout: 300s
  read_header_timeout: 10s
  write_timeout: 300s
  idle_timeout: 60s
  shutdown_timeout: 15s
  trusted_proxy_cidrs:
    - "127.0.0.1/32"

metrics:
  bearer_token_env: "RELEASE_SMOKE_METRICS_TOKEN"
  allowed_cidrs: []

auth:
  rate_limit_qps: 100
  rate_limit_burst: 100
  tokens:
    - name: "internal-trial"
      value_env: "RELEASE_SMOKE_GATEWAY_TOKEN"
      rate_limit_qps: 20
      rate_limit_burst: 20

upstream:
  retryable_status_codes: [429, 500, 502, 503, 504]
  retry_backoff: 200ms
  default_max_retries: 0
  health_check_interval: 24h
  health_check_timeout: 2s
  breaker_interval: 30s
  breaker_timeout: 5s
  breaker_failure_ratio: 0.5
  breaker_minimum_requests: 5
  breaker_half_open_requests: 3
  default_timeout_non_stream: ${timeout_override}
  default_timeout_stream: 30s

providers:
EOF

  if [[ "$HAVE_OPENAI_KEY" == "1" ]]; then
    cat >>"$CONFIG_FILE" <<EOF
  - name: "openai"
    base_url: "https://api.openai.com"
    api_key_env: "RELEASE_REAL_OPENAI_KEY"
    models:
      - "${OPENAI_MODEL}"
EOF
  fi

  if [[ "$HAVE_ANTHROPIC_KEY" == "1" ]]; then
    cat >>"$CONFIG_FILE" <<EOF
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    api_key_env: "RELEASE_REAL_ANTHROPIC_KEY"
    models:
      - "${ANTHROPIC_MODEL}"
EOF
  fi

  if [[ "$HAVE_SILICONFLOW_KEY" == "1" ]]; then
    cat >>"$CONFIG_FILE" <<EOF
  - name: "siliconflow"
    base_url: "https://api.siliconflow.cn"
    api_key_env: "RELEASE_REAL_SILICONFLOW_KEY"
    models:
      - "${SILICONFLOW_MODEL}"
EOF
  fi
}

start_gateway() {
  local openai_key_value="$1"
  local anthropic_key_value="$2"
  local siliconflow_key_value="$3"
  local -a gateway_env_prefix=()
  if [[ "$REAL_SMOKE_CLEAR_PROXY" == "1" ]]; then
    gateway_env_prefix=(env -u all_proxy -u ALL_PROXY -u http_proxy -u HTTP_PROXY -u https_proxy -u HTTPS_PROXY)
  fi
  : >"$LOG_FILE"
  "${gateway_env_prefix[@]}" \
    RELEASE_SMOKE_METRICS_TOKEN="$METRICS_TOKEN" \
    RELEASE_SMOKE_GATEWAY_TOKEN="$GATEWAY_TOKEN" \
    RELEASE_REAL_OPENAI_KEY="$openai_key_value" \
    RELEASE_REAL_ANTHROPIC_KEY="$anthropic_key_value" \
    RELEASE_REAL_SILICONFLOW_KEY="$siliconflow_key_value" \
    GATEWAY_CONFIG_PATH="$CONFIG_FILE" \
    "$GATEWAY_BIN" >"$LOG_FILE" 2>&1 &
  GW_PID="$!"
  wait_live "http://127.0.0.1:${PORT}" || fail "gateway did not become ready"
}

stop_gateway() {
  if [[ -n "$GW_PID" ]] && kill -0 "$GW_PID" >/dev/null 2>&1; then
    kill -TERM "$GW_PID" >/dev/null 2>&1 || true
    wait "$GW_PID" >/dev/null 2>&1 || true
  fi
  GW_PID=""
}

chat_request() {
  local model="$1"
  local stream="$2"
  local output="$3"
  curl --noproxy '*' -sS ${stream:+-N} -o "$output" -w "%{http_code}" \
    -H "Authorization: Bearer ${GATEWAY_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"${model}\",\"stream\":${stream:-false},\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly OK\"}],\"max_tokens\":16,\"temperature\":0}" \
    "http://127.0.0.1:${PORT}/v1/chat/completions"
}

choose_model() {
  if [[ "$REAL_SMOKE_PROVIDER" == "siliconflow" && "$HAVE_SILICONFLOW_KEY" == "1" ]]; then
    printf '%s\n' "$SILICONFLOW_MODEL"
    return 0
  fi
  if [[ "$REAL_SMOKE_PROVIDER" == "anthropic" && "$HAVE_ANTHROPIC_KEY" == "1" ]]; then
    printf '%s\n' "$ANTHROPIC_MODEL"
    return 0
  fi
  if [[ "$REAL_SMOKE_PROVIDER" == "openai" && "$HAVE_OPENAI_KEY" == "1" ]]; then
    printf '%s\n' "$OPENAI_MODEL"
    return 0
  fi
  if [[ "$HAVE_OPENAI_KEY" == "1" ]]; then
    printf '%s\n' "$OPENAI_MODEL"
    return 0
  fi
  if [[ "$HAVE_ANTHROPIC_KEY" == "1" ]]; then
    printf '%s\n' "$ANTHROPIC_MODEL"
    return 0
  fi
  if [[ "$HAVE_SILICONFLOW_KEY" == "1" ]]; then
    printf '%s\n' "$SILICONFLOW_MODEL"
    return 0
  fi
  return 1
}

if [[ "$HAVE_OPENAI_KEY" != "1" && "$HAVE_ANTHROPIC_KEY" != "1" && "$HAVE_SILICONFLOW_KEY" != "1" ]]; then
  fail "set OPENAI_API_KEY or ANTHROPIC_API_KEY or SILICONFLOW_API_KEY"
fi

PRIMARY_MODEL="$(choose_model)" || fail "no real provider model available"
write_config "30s"
start_gateway "${OPENAI_API_KEY:-invalid-provider-key}" "${ANTHROPIC_API_KEY:-invalid-provider-key}" "${SILICONFLOW_API_KEY:-invalid-provider-key}"

GATEWAY_URL="http://127.0.0.1:${PORT}"
[[ "$(status_code "${GATEWAY_URL}/health/live")" == "200" ]] || fail "/health/live check failed"
say "ok  /health/live -> 200"

[[ "$(status_code "${GATEWAY_URL}/metrics" || true)" == "403" ]] || fail "/metrics without auth should be 403"
say "ok  /metrics without auth -> 403"

[[ "$(status_code -H "Authorization: Bearer ${METRICS_TOKEN}" "${GATEWAY_URL}/metrics" || true)" == "200" ]] || fail "/metrics with auth should be 200"
say "ok  /metrics with auth -> 200"

[[ "$(status_code -H "Content-Type: application/json" -d "{\"model\":\"${PRIMARY_MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}]}" "${GATEWAY_URL}/v1/chat/completions" || true)" == "401" ]] || fail "unauthenticated chat should be 401"
say "ok  unauthenticated /v1/chat/completions -> 401"

RESP_FILE="${WORK_DIR}/real-chat.json"
HTTP_CODE="$(chat_request "$PRIMARY_MODEL" "" "$RESP_FILE" || true)"
[[ "$HTTP_CODE" == "200" ]] || fail "real provider non-stream chat returned ${HTTP_CODE}"
grep -q '"choices"' "$RESP_FILE" || fail "real provider non-stream response missing choices"
say "ok  real provider non-stream -> 200"

STREAM_FILE="${WORK_DIR}/real-stream.txt"
HTTP_CODE="$(chat_request "$PRIMARY_MODEL" "true" "$STREAM_FILE" || true)"
[[ "$HTTP_CODE" == "200" ]] || fail "real provider stream chat returned ${HTTP_CODE}"
grep -q '^data:' "$STREAM_FILE" || fail "real provider stream response missing data frames"
grep -q '\[DONE\]' "$STREAM_FILE" || fail "real provider stream response missing [DONE]"
say "ok  real provider stream -> 200 with SSE frames"

stop_gateway
write_config "1ms"
start_gateway "${OPENAI_API_KEY:-invalid-provider-key}" "${ANTHROPIC_API_KEY:-invalid-provider-key}" "${SILICONFLOW_API_KEY:-invalid-provider-key}"

TIMEOUT_FILE="${WORK_DIR}/timeout.json"
HTTP_CODE="$(chat_request "$PRIMARY_MODEL" "" "$TIMEOUT_FILE" || true)"
[[ "$HTTP_CODE" == "504" ]] || fail "forced timeout check returned ${HTTP_CODE}, want 504"
say "ok  forced upstream timeout -> 504"

stop_gateway
write_config "30s"
start_gateway "invalid-provider-key" "invalid-provider-key" "invalid-provider-key"

FAIL_FILE="${WORK_DIR}/upstream-fail.json"
HTTP_CODE="$(chat_request "$PRIMARY_MODEL" "" "$FAIL_FILE" || true)"
[[ "$HTTP_CODE" == "503" || "$HTTP_CODE" == "401" || "$HTTP_CODE" == "403" ]] || fail "forced upstream failure returned ${HTTP_CODE}"
[[ "$(status_code "${GATEWAY_URL}/health/live")" == "200" ]] || fail "gateway did not stay live after upstream failure"
say "ok  forced upstream failure stays bounded"

say "real provider smoke complete"
say "gateway log: ${LOG_FILE}"
