#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GOCACHE="${GOCACHE:-/tmp/strait-gocache}"
GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
GOSUMDB="${GOSUMDB:-sum.golang.org}"

REPORT_DIR="${REPORT_DIR:-ops/release/reports}"
mkdir -p build "$REPORT_DIR"

NORMAL_QPS="${SELFTEST_NORMAL_QPS:-500}"
PEAK_QPS="${SELFTEST_PEAK_QPS:-5000}"
DURATION="${SELFTEST_DURATION:-1m}"
CONCURRENCY="${SELFTEST_CONCURRENCY:-5000}"
PROVIDERS="${SELFTEST_PROVIDERS:-10}"
REQUEST_BYTES="${SELFTEST_REQUEST_BYTES:-262144}"
RESPONSE_BYTES="${SELFTEST_RESPONSE_BYTES:-1048576}"
BASELINE_SAMPLES="${SELFTEST_BASELINE_SAMPLES:-40}"
STREAM_RATIO="${SELFTEST_STREAM_RATIO:-0.5}"
RUN_NORMAL="${SELFTEST_RUN_NORMAL:-1}"
RUN_PEAK="${SELFTEST_RUN_PEAK:-1}"

timestamp="$(date +%Y%m%d-%H%M%S)"
gateway_bin="${ROOT_DIR}/build/strait"

echo "building gateway binary -> ${gateway_bin}"
env GOCACHE="$GOCACHE" GOPROXY="$GOPROXY" GOSUMDB="$GOSUMDB" \
  go build -trimpath -ldflags "-s -w" -o "$gateway_bin" ./cmd/gateway/main.go

run_phase() {
  local name="$1"
  local qps="$2"
  local report_path="${REPORT_DIR}/selftest-${name}-${timestamp}.json"

  echo "running ${name} selftest: qps=${qps} duration=${DURATION} concurrency=${CONCURRENCY}"
  env GOCACHE="$GOCACHE" GOPROXY="$GOPROXY" GOSUMDB="$GOSUMDB" \
    go run ./cmd/releasecheck \
      -gateway-bin "$gateway_bin" \
      -report "$report_path" \
      -qps "$qps" \
      -duration "$DURATION" \
      -concurrency "$CONCURRENCY" \
      -providers "$PROVIDERS" \
      -request-bytes "$REQUEST_BYTES" \
      -response-bytes "$RESPONSE_BYTES" \
      -baseline-samples "$BASELINE_SAMPLES" \
      -stream-ratio "$STREAM_RATIO"

  echo "report written: ${report_path}"
}

if [[ "$RUN_NORMAL" == "1" ]]; then
  run_phase normal "$NORMAL_QPS"
fi

if [[ "$RUN_PEAK" == "1" ]]; then
  run_phase peak "$PEAK_QPS"
fi
