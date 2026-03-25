package middleware

import (
	"bytes"
	"sort"
	"strings"
	"testing"
	"time"
)

var (
	benchmarkBodyPrompt            = strings.Repeat("hello from strait ", 64)
	benchmarkBodyStream            = []byte(`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"system","content":"keep responses terse"},{"role":"user","content":"` + benchmarkBodyPrompt + `"}]}`)
	benchmarkBodyStreamWithOptions = []byte(`{
  "model":"gpt-4o-mini",
  "stream":true,
  "messages":[{"role":"user","content":"` + benchmarkBodyPrompt + `"}],
  "stream_options":{"foo":"bar"}
}`)
)

func BenchmarkHotPath_BodyParseMeta_Stream(b *testing.B) {
	b.SetBytes(int64(len(benchmarkBodyStream)))
	runLatencySampleBenchmark(b, func() {
		model, isStream, err := extractRequestBodyMeta(benchmarkBodyStream)
		if err != nil {
			b.Fatalf("extractRequestBodyMeta: %v", err)
		}
		if model != "gpt-4o-mini" || !isStream {
			b.Fatalf("unexpected meta: model=%q stream=%v", model, isStream)
		}
	})
}

func BenchmarkHotPath_BodyBuildUpstream_Stream(b *testing.B) {
	b.SetBytes(int64(len(benchmarkBodyStream)))
	runLatencySampleBenchmark(b, func() {
		upstream, injected := buildUpstreamBody(benchmarkBodyStream, true)
		if !injected {
			b.Fatal("expected include_usage injection")
		}
		if !bytes.Contains(upstream, []byte(`"stream_options":{"include_usage":true}`)) {
			b.Fatal("missing stream_options.include_usage")
		}
	})
}

func BenchmarkHotPath_BodyBuildUpstream_StreamOptions(b *testing.B) {
	b.SetBytes(int64(len(benchmarkBodyStreamWithOptions)))
	runLatencySampleBenchmark(b, func() {
		upstream, injected := buildUpstreamBody(benchmarkBodyStreamWithOptions, true)
		if !injected {
			b.Fatal("expected stream_options object injection")
		}
		if !bytes.Contains(upstream, []byte(`"include_usage":true`)) {
			b.Fatal("missing include_usage in stream_options")
		}
	})
}

func runLatencySampleBenchmark(b *testing.B, fn func()) {
	b.Helper()

	const sampleMask = 63
	samples := make([]int64, 0, maxInt(1, b.N/64))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i&sampleMask == 0 {
			start := time.Now()
			fn()
			samples = append(samples, time.Since(start).Nanoseconds())
			continue
		}
		fn()
	}
	b.StopTimer()

	reportLatencySampleMetrics(b, samples)
}

func reportLatencySampleMetrics(b *testing.B, samples []int64) {
	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool {
		return samples[i] < samples[j]
	})
	b.ReportMetric(float64(samplePercentile(samples, 99, 100)), "sample_p99_ns/op")
	b.ReportMetric(float64(samplePercentile(samples, 999, 1000)), "sample_p999_ns/op")
}

func samplePercentile(samples []int64, numerator, denominator int) int64 {
	if len(samples) == 0 {
		return 0
	}
	idx := (len(samples)*numerator + denominator - 1) / denominator
	if idx <= 0 {
		return samples[0]
	}
	if idx > len(samples) {
		return samples[len(samples)-1]
	}
	return samples[idx-1]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
