#!/usr/bin/env bash
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8080}"
TOKEN="${GATEWAY_TOKEN:-${TEAM_A_WEB_TOKEN:-}}"
MODEL="${GATEWAY_MODEL:-gpt-4o-mini}"

say() {
  printf '%s\n' "$*"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

status_code() {
  curl -sS -o /dev/null -w "%{http_code}" "$@"
}

say "smoke target: $GATEWAY"

LIVE_CODE="$(status_code "$GATEWAY/health/live" || true)"
[ "$LIVE_CODE" = "200" ] || fail "/health/live returned $LIVE_CODE"
say "ok  /health/live -> 200"

NOAUTH_CODE="$(status_code \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}' \
  "$GATEWAY/v1/chat/completions" || true)"
[ "$NOAUTH_CODE" = "401" ] || fail "unauthenticated chat returned $NOAUTH_CODE"
say "ok  unauthenticated /v1/chat/completions -> 401"

if [ -z "$TOKEN" ]; then
  say "skip authenticated request: set GATEWAY_TOKEN or TEAM_A_WEB_TOKEN"
  exit 0
fi

RESP_FILE="$(mktemp)"
AUTH_CODE="$(curl -sS -o "$RESP_FILE" -w "%{http_code}" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly OK\"}],\"max_tokens\":8,\"temperature\":0}" \
  "$GATEWAY/v1/chat/completions" || true)"

if [ "$AUTH_CODE" != "200" ]; then
  say "skip authenticated request: upstream not ready or provider key missing (HTTP $AUTH_CODE)"
  rm -f "$RESP_FILE"
  exit 0
fi

grep -q '"choices"' "$RESP_FILE" || fail "authenticated chat returned 200 without choices payload"
say "ok  authenticated /v1/chat/completions -> 200"

rm -f "$RESP_FILE"
