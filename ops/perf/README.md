# Hot Path Performance Guardrails

This repository keeps hard budgets for the three hottest paths:

- `body parse`: request metadata scan and `stream_options.include_usage` injection
- `anthropic translate`: non-stream Anthropic -> OpenAI response translation
- `sse translate`: Anthropic SSE -> OpenAI chunk translation

Run the guardrail locally:

```bash
make perf-hotpath
```

The script does three things:

1. Runs focused microbenchmarks with `-benchmem`
2. Writes CPU and heap profiles under `ops/perf/profiles/`
3. Fails if any benchmark crosses `ops/perf/budgets.tsv`

Budget notes:

- `B/op` and `allocs/op` are hard regression guards.
- `sample_p99_ns/op` and `sample_p999_ns/op` are sampled microbenchmark percentiles, not production SLOs.
- Real request tail latency still comes from Prometheus histograms and `ops/prometheus/recording_rules.yml`.

To inspect a profile after a run:

```bash
go tool pprof -http=:0 ops/perf/profiles/adapter.cpu.pprof
go tool pprof -http=:0 ops/perf/profiles/body.mem.pprof
```
