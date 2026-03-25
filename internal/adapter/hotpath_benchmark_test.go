package adapter

import (
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

var (
	benchmarkOpenAIAnthropicText = []byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"system","content":"be concise"},{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"tell me more"}],"max_tokens":256,"temperature":0.3,"stream":true,"top_p":0.9,"stop":["<END>"]}`)
	benchmarkOpenAIAnthropicTool = []byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"What's the weather?"},{"role":"assistant","content":"I'll check.","tool_calls":[{"id":"toolu_perf","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},{"role":"tool","tool_call_id":"toolu_perf","content":"72F"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`)
	benchmarkAnthropicText       = []byte(`{"id":"msg_perf_text","type":"message","role":"assistant","model":"claude-3-5-sonnet","content":[{"type":"text","text":"` + strings.Repeat("hello from anthropic ", 96) + `"}],"stop_reason":"end_turn","usage":{"input_tokens":128,"output_tokens":64}}`)
	benchmarkAnthropicTool       = []byte(`{"id":"msg_perf_tool","type":"message","role":"assistant","model":"claude-3-5-sonnet","content":[{"type":"text","text":"calling tool"},{"type":"tool_use","id":"toolu_perf","name":"search","input":{"query":"` + strings.Repeat("alpha ", 32) + `","limit":8}}],"stop_reason":"tool_use","usage":{"input_tokens":96,"output_tokens":32}}`)
	benchmarkAnthropicSSEText    = strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_sse_text","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + strings.Repeat("hello ", 16) + `"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":16}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	benchmarkAnthropicSSETool = strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_sse_tool","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_perf","name":"search","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"` + strings.Repeat(`{\"query\":\"abcdefgh\"}`, 16) + `"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":16}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
)

func BenchmarkHotPath_TranslateOpenAIToAnthropic_Text(b *testing.B) {
	b.SetBytes(int64(len(benchmarkOpenAIAnthropicText)))
	runAdapterLatencySampleBenchmark(b, func() {
		out, err := TranslateOpenAIToAnthropicBody(benchmarkOpenAIAnthropicText)
		if err != nil {
			b.Fatalf("TranslateOpenAIToAnthropicBody text: %v", err)
		}
		if len(out) == 0 {
			b.Fatal("empty translation output")
		}
	})
}

func BenchmarkHotPath_TranslateOpenAIToAnthropic_ToolUse(b *testing.B) {
	b.SetBytes(int64(len(benchmarkOpenAIAnthropicTool)))
	runAdapterLatencySampleBenchmark(b, func() {
		out, err := TranslateOpenAIToAnthropicBody(benchmarkOpenAIAnthropicTool)
		if err != nil {
			b.Fatalf("TranslateOpenAIToAnthropicBody tool: %v", err)
		}
		if len(out) == 0 {
			b.Fatal("empty translation output")
		}
	})
}

func BenchmarkHotPath_TranslateAnthropicToOpenAI_Text(b *testing.B) {
	b.SetBytes(int64(len(benchmarkAnthropicText)))
	runAdapterLatencySampleBenchmark(b, func() {
		out, err := TranslateAnthropicToOpenAI(benchmarkAnthropicText)
		if err != nil {
			b.Fatalf("TranslateAnthropicToOpenAI text: %v", err)
		}
		if len(out) == 0 {
			b.Fatal("empty translation output")
		}
	})
}

func BenchmarkHotPath_TranslateAnthropicToOpenAI_ToolUse(b *testing.B) {
	b.SetBytes(int64(len(benchmarkAnthropicTool)))
	runAdapterLatencySampleBenchmark(b, func() {
		out, err := TranslateAnthropicToOpenAI(benchmarkAnthropicTool)
		if err != nil {
			b.Fatalf("TranslateAnthropicToOpenAI tool: %v", err)
		}
		if len(out) == 0 {
			b.Fatal("empty translation output")
		}
	})
}

func BenchmarkHotPath_TranslateAnthropicStream_Text(b *testing.B) {
	b.SetBytes(int64(len(benchmarkAnthropicSSEText)))
	writer := &discardStreamWriter{}
	runAdapterLatencySampleBenchmark(b, func() {
		writer.Reset()
		resp := benchmarkSSEResponse(benchmarkAnthropicSSEText)
		if err := TranslateAnthropicStream(resp, writer, "claude-3-5-sonnet"); err != nil {
			b.Fatalf("TranslateAnthropicStream text: %v", err)
		}
	})
}

func BenchmarkHotPath_TranslateAnthropicStream_ToolDelta(b *testing.B) {
	b.SetBytes(int64(len(benchmarkAnthropicSSETool)))
	writer := &discardStreamWriter{}
	runAdapterLatencySampleBenchmark(b, func() {
		writer.Reset()
		resp := benchmarkSSEResponse(benchmarkAnthropicSSETool)
		if err := TranslateAnthropicStream(resp, writer, "claude-3-5-sonnet"); err != nil {
			b.Fatalf("TranslateAnthropicStream tool: %v", err)
		}
	})
}

func benchmarkSSEResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type discardStreamWriter struct {
	header http.Header
	status int
}

func (w *discardStreamWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *discardStreamWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *discardStreamWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *discardStreamWriter) Flush() {}

func (w *discardStreamWriter) Reset() {
	w.status = 0
	for key := range w.header {
		delete(w.header, key)
	}
}

func runAdapterLatencySampleBenchmark(b *testing.B, fn func()) {
	b.Helper()

	const sampleMask = 63
	samples := make([]int64, 0, maxAdapterInt(1, b.N/64))

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

	reportAdapterLatencySampleMetrics(b, samples)
}

func reportAdapterLatencySampleMetrics(b *testing.B, samples []int64) {
	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool {
		return samples[i] < samples[j]
	})
	b.ReportMetric(float64(adapterSamplePercentile(samples, 99, 100)), "sample_p99_ns/op")
	b.ReportMetric(float64(adapterSamplePercentile(samples, 999, 1000)), "sample_p999_ns/op")
}

func adapterSamplePercentile(samples []int64, numerator, denominator int) int64 {
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

func maxAdapterInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
