#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE_DIR="${ROOT_DIR}/ops/perf/profiles"
OUTPUT_FILE="${ROOT_DIR}/ops/perf/latest.txt"
BUDGET_FILE="${ROOT_DIR}/ops/perf/budgets.tsv"

mkdir -p "${PROFILE_DIR}"
: > "${OUTPUT_FILE}"

run_bench() {
	local pkg="$1"
	local prefix="$2"
	local tmp

	tmp="$(mktemp)"
	go test -count=1 -run '^$' -bench '^BenchmarkHotPath_' -benchmem \
		-cpuprofile "${PROFILE_DIR}/${prefix}.cpu.pprof" \
		-memprofile "${PROFILE_DIR}/${prefix}.mem.pprof" \
		"${pkg}" | tee "${tmp}"
	cat "${tmp}" >> "${OUTPUT_FILE}"
	rm -f "${tmp}"
}

run_bench ./internal/middleware body
run_bench ./internal/adapter adapter

awk '
NR == FNR {
	if ($1 ~ /^#/ || NF < 3) {
		next
	}
	budgets[$1 SUBSEP $2] = $3 + 0
	next
}
$1 ~ /^BenchmarkHotPath_/ {
	benchmark = $1
	sub(/-[0-9]+$/, "", benchmark)
	for (i = 3; i <= NF - 1; i += 2) {
		seen[benchmark SUBSEP $(i + 1)] = $i + 0
	}
}
END {
	status = 0
	for (key in budgets) {
		split(key, parts, SUBSEP)
		benchmark = parts[1]
		metric = parts[2]

		if (!(key in seen)) {
			printf("missing metric: %s %s\n", benchmark, metric) > "/dev/stderr"
			status = 1
			continue
		}

		if (seen[key] > budgets[key]) {
			printf("budget exceeded: %s %s got=%.0f max=%.0f\n", benchmark, metric, seen[key], budgets[key]) > "/dev/stderr"
			status = 1
		}
	}
	exit status
}
' "${BUDGET_FILE}" "${OUTPUT_FILE}"

printf 'profiles written to %s\n' "${PROFILE_DIR}"
printf 'benchmark snapshot written to %s\n' "${OUTPUT_FILE}"
