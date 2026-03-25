package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeSSEResponse builds a fake *http.Response whose body contains raw SSE text.
func makeSSEResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// parseSSEChunks splits the response body into individual data payloads.
func parseSSEChunks(t *testing.T, body string) []map[string]any {
	t.Helper()
	var chunks []map[string]any
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatalf("failed to parse SSE chunk %q: %v", payload, err)
		}
		chunks = append(chunks, m)
	}
	return chunks
}

func choiceDelta(chunk map[string]any) map[string]any {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	return delta
}

func choiceFinishReason(chunk map[string]any) any {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	return choice["finish_reason"]
}

// ---------------------------------------------------------------------------
// Text streaming
// ---------------------------------------------------------------------------

func TestTranslateAnthropicStream_TextOnly(t *testing.T) {
	sseBody := `event: message_start
data: {"type":"message_start","message":{"id":"msg_stream_01","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", world!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":8}}

event: message_stop
data: {"type":"message_stop"}

`
	resp := makeSSEResponse(sseBody)
	w := httptest.NewRecorder()

	if err := TranslateAnthropicStream(resp, w, "claude-3-5-sonnet-20241022"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := w.Body.String()

	// Must end with [DONE]
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("response missing [DONE]")
	}

	// Must be SSE content-type
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	chunks := parseSSEChunks(t, body)

	// First chunk should establish role
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	firstDelta := choiceDelta(chunks[0])
	if firstDelta["role"] != "assistant" {
		t.Errorf("first delta role = %v, want assistant", firstDelta["role"])
	}

	// Collect all text deltas
	var text string
	for _, c := range chunks {
		d := choiceDelta(c)
		if s, ok := d["content"].(string); ok {
			text += s
		}
	}
	if text != "Hello, world!" {
		t.Errorf("combined text = %q, want %q", text, "Hello, world!")
	}

	// Last data chunk should carry finish_reason=stop
	last := chunks[len(chunks)-1]
	if fr := choiceFinishReason(last); fr != "stop" {
		t.Errorf("finish_reason = %v, want stop", fr)
	}

	// model and id should be populated from message_start
	if chunks[0]["id"] != "msg_stream_01" {
		t.Errorf("id = %v, want msg_stream_01", chunks[0]["id"])
	}
	if chunks[0]["model"] != "claude-3-5-sonnet-20241022" {
		t.Errorf("model = %v, want claude-3-5-sonnet-20241022", chunks[0]["model"])
	}
}

// ---------------------------------------------------------------------------
// Tool-use streaming
// ---------------------------------------------------------------------------

func TestTranslateAnthropicStream_ToolUse(t *testing.T) {
	sseBody := `event: message_start
data: {"type":"message_start","message":{"id":"msg_tool_stream","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":50,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_stream_01","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"NYC\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":30}}

event: message_stop
data: {"type":"message_stop"}

`
	resp := makeSSEResponse(sseBody)
	w := httptest.NewRecorder()

	if err := TranslateAnthropicStream(resp, w, "claude-3-5-sonnet-20241022"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunks := parseSSEChunks(t, w.Body.String())

	// Find the chunk with the tool call header (should have id/name)
	var foundHeader bool
	var accArgs string
	for _, c := range chunks {
		d := choiceDelta(c)
		tcs, ok := d["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, rawTC := range tcs {
			tc := rawTC.(map[string]any)
			if id, ok := tc["id"].(string); ok && id == "toolu_stream_01" {
				foundHeader = true
				fn := tc["function"].(map[string]any)
				if fn["name"] != "get_weather" {
					t.Errorf("tool name = %v, want get_weather", fn["name"])
				}
			}
			if fn, ok := tc["function"].(map[string]any); ok {
				if args, ok := fn["arguments"].(string); ok {
					accArgs += args
				}
			}
		}
	}

	if !foundHeader {
		t.Error("no tool call header chunk found")
	}
	if accArgs != `{"location":"NYC"}` {
		t.Errorf("accumulated arguments = %q, want %q", accArgs, `{"location":"NYC"}`)
	}

	// finish_reason must be tool_calls
	var lastFinish any
	for _, c := range chunks {
		if fr := choiceFinishReason(c); fr != nil {
			lastFinish = fr
		}
	}
	if lastFinish != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", lastFinish)
	}
}

// ---------------------------------------------------------------------------
// Finish-reason mapping
// ---------------------------------------------------------------------------

func TestTranslateAnthropicStream_MaxTokensFinishReason(t *testing.T) {
	sseBody := `event: message_start
data: {"type":"message_start","message":{"id":"msg_maxlen","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"truncated"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":10}}

event: message_stop
data: {"type":"message_stop"}

`
	w := httptest.NewRecorder()
	if err := TranslateAnthropicStream(makeSSEResponse(sseBody), w, "claude"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunks := parseSSEChunks(t, w.Body.String())
	var lastFinish any
	for _, c := range chunks {
		if fr := choiceFinishReason(c); fr != nil {
			lastFinish = fr
		}
	}
	if lastFinish != "length" {
		t.Errorf("finish_reason = %v, want length", lastFinish)
	}
}

func TestTranslateAnthropicStream_FallsBackToJSONTypeWithoutEventHeader(t *testing.T) {
	sseBody := `data: {"type":"message_start","message":{"id":"msg_no_event","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":null}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

`
	w := httptest.NewRecorder()
	if err := TranslateAnthropicStream(makeSSEResponse(sseBody), w, "claude"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunks := parseSSEChunks(t, w.Body.String())
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	if got := choiceDelta(chunks[0])["role"]; got != "assistant" {
		t.Fatalf("first delta role = %v, want assistant", got)
	}
	if got := choiceDelta(chunks[1])["content"]; got != "hello" {
		t.Fatalf("content = %v, want hello", got)
	}
	if got := choiceFinishReason(chunks[2]); got != "stop" {
		t.Fatalf("finish_reason = %v, want stop", got)
	}
}

// ---------------------------------------------------------------------------
// Empty stream (only message_stop)
// ---------------------------------------------------------------------------

func TestTranslateAnthropicStream_EmptyStream(t *testing.T) {
	sseBody := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	w := httptest.NewRecorder()
	if err := TranslateAnthropicStream(makeSSEResponse(sseBody), w, "claude"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Body.String(), "data: [DONE]") {
		t.Error("expected [DONE] even for empty stream")
	}
}

func TestTranslateAnthropicStream_LargeToolDelta(t *testing.T) {
	largeJSONChunk := strings.Repeat("x", 600000)
	sseBody := fmt.Sprintf(`event: message_start
data: {"type":"message_start","message":{"id":"msg_large","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_large","name":"large_tool","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"%s"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

event: message_stop
data: {"type":"message_stop"}

`, largeJSONChunk)

	w := httptest.NewRecorder()
	if err := TranslateAnthropicStream(makeSSEResponse(sseBody), w, "claude"); err != nil {
		t.Fatalf("unexpected error for large tool delta: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "toolu_large") {
		t.Fatal("expected translated tool call header in output")
	}
	if !strings.Contains(body, largeJSONChunk) {
		t.Fatal("expected large tool delta to be preserved in output")
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatal("expected [DONE] in output")
	}
}
