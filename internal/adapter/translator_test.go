package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fxwio/strait/internal/model"
)

func readAnthropicRequest(t *testing.T, body []byte) AnthropicRequest {
	t.Helper()
	var out AnthropicRequest
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal anthropic request: %v", err)
	}
	return out
}

func translateAnthropicRequest(t *testing.T, payload string) AnthropicRequest {
	t.Helper()
	body, err := TranslateOpenAIToAnthropicBody([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return readAnthropicRequest(t, body)
}

func TestTranslateOpenAIToAnthropic_BasicConversion(t *testing.T) {
	payload := `{
		"model": "claude-3-5-sonnet-20241022",
		"messages": [{"role": "user", "content": "Hello"}],
		"max_tokens": 1024,
		"temperature": 0.7
	}`
	out := translateAnthropicRequest(t, payload)

	if out.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("model = %q, want %q", out.Model, "claude-3-5-sonnet-20241022")
	}
	if out.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want 1024", out.MaxTokens)
	}
	if out.Temperature != 0.7 {
		t.Errorf("temperature = %f, want 0.7", out.Temperature)
	}
	if len(out.Messages) != 1 || out.Messages[0].Role != "user" || out.Messages[0].Content != "Hello" {
		t.Errorf("unexpected messages: %+v", out.Messages)
	}
}

func TestTranslateOpenAIToAnthropic_SystemMessageExtracted(t *testing.T) {
	payload := `{
		"model": "claude-3-5-sonnet-20241022",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Hi"}
		],
		"max_tokens": 512
	}`
	out := translateAnthropicRequest(t, payload)

	if out.System != "You are a helpful assistant." {
		t.Errorf("system = %q, want %q", out.System, "You are a helpful assistant.")
	}
	if len(out.Messages) != 1 {
		t.Errorf("expected 1 message (no system), got %d", len(out.Messages))
	}
	if out.Messages[0].Role != "user" {
		t.Errorf("message role = %q, want %q", out.Messages[0].Role, "user")
	}
}

func TestTranslateOpenAIToAnthropic_DefaultMaxTokens(t *testing.T) {
	payload := `{
		"model": "claude-3-5-sonnet-20241022",
		"messages": [{"role": "user", "content": "Hi"}]
	}`
	out := translateAnthropicRequest(t, payload)

	if out.MaxTokens != 4096 {
		t.Errorf("expected default max_tokens=4096, got %d", out.MaxTokens)
	}
}

func TestTranslateOpenAIToAnthropic_StreamField(t *testing.T) {
	payload := `{
		"model": "claude-3-5-sonnet-20241022",
		"messages": [{"role": "user", "content": "Hi"}],
		"stream": true
	}`
	out := translateAnthropicRequest(t, payload)

	if !out.Stream {
		t.Error("expected stream=true")
	}
}

func TestTranslateOpenAIToAnthropic_InvalidJSON(t *testing.T) {
	if _, err := TranslateOpenAIToAnthropicBody([]byte(`not valid json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestTranslateOpenAIToAnthropic_EmptyBody(t *testing.T) {
	if _, err := TranslateOpenAIToAnthropicBody(nil); err == nil {
		t.Error("expected error for empty body")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// response_format adaptation tests
// ────────────────────────────────────────────────────────────────────────────

func TestTranslateOpenAIToAnthropic_ResponseFormat_None(t *testing.T) {
	// No response_format → system prompt must not be modified.
	payload := `{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`
	out := translateAnthropicRequest(t, payload)
	if out.System != "" {
		t.Errorf("system prompt modified when no response_format: %q", out.System)
	}
}

func TestTranslateOpenAIToAnthropic_ResponseFormat_JsonObject(t *testing.T) {
	payload := `{
		"model":"claude-3-5-sonnet-20241022",
		"messages":[{"role":"user","content":"List three colours."}],
		"response_format":{"type":"json_object"}
	}`
	out := translateAnthropicRequest(t, payload)
	if !strings.Contains(out.System, "JSON") {
		t.Errorf("system prompt missing JSON directive: %q", out.System)
	}
}

func TestTranslateOpenAIToAnthropic_ResponseFormat_JsonObject_ExistingSystem(t *testing.T) {
	// When the client already has a system message, the JSON directive must
	// be appended, not replace it.
	payload := `{
		"model":"claude-3-5-sonnet-20241022",
		"messages":[
			{"role":"system","content":"Be concise."},
			{"role":"user","content":"List colours."}
		],
		"response_format":{"type":"json_object"}
	}`
	out := translateAnthropicRequest(t, payload)
	if !strings.Contains(out.System, "Be concise") {
		t.Errorf("original system content lost: %q", out.System)
	}
	if !strings.Contains(out.System, "JSON") {
		t.Errorf("JSON directive missing: %q", out.System)
	}
}

func TestTranslateOpenAIToAnthropic_ResponseFormat_JsonSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`
	payload := `{
		"model":"claude-3-5-sonnet-20241022",
		"messages":[{"role":"user","content":"Give me a person."}],
		"response_format":{
			"type":"json_schema",
			"json_schema":{
				"name":"person",
				"description":"A person object",
				"schema":` + schema + `,
				"strict":true
			}
		}
	}`
	out := translateAnthropicRequest(t, payload)
	if !strings.Contains(out.System, "JSON Schema") {
		t.Errorf("system prompt missing 'JSON Schema': %q", out.System)
	}
	if !strings.Contains(out.System, "name") {
		t.Errorf("system prompt missing schema content: %q", out.System)
	}
}

func TestTranslateOpenAIToAnthropic_ResponseFormat_JsonSchema_NoSchema(t *testing.T) {
	// json_schema without a schema body → fall back to json_object directive.
	payload := `{
		"model":"claude-3-5-sonnet-20241022",
		"messages":[{"role":"user","content":"hi"}],
		"response_format":{
			"type":"json_schema",
			"json_schema":{"name":"empty_schema"}
		}
	}`
	out := translateAnthropicRequest(t, payload)
	if !strings.Contains(out.System, "empty_schema") {
		t.Errorf("system prompt missing schema name: %q", out.System)
	}
}

func TestTranslateOpenAIToAnthropic_ResponseFormat_Text(t *testing.T) {
	// type:"text" is the default — no injection expected.
	payload := `{
		"model":"claude-3-5-sonnet-20241022",
		"messages":[{"role":"user","content":"hi"}],
		"response_format":{"type":"text"}
	}`
	out := translateAnthropicRequest(t, payload)
	if out.System != "" {
		t.Errorf("unexpected system prompt for type=text: %q", out.System)
	}
}

func TestAdaptResponseFormatForAnthropic_NilFormat(t *testing.T) {
	anthropicReq := AnthropicRequest{System: "original"}
	adaptResponseFormatForAnthropic(nil, &anthropicReq)
	if anthropicReq.System != "original" {
		t.Errorf("nil format modified system: %q", anthropicReq.System)
	}
}

func TestAdaptResponseFormatForAnthropic_JsonSchemaNilJSONSchema(t *testing.T) {
	// json_schema with nil JSONSchema field → treat as json_object.
	rf := &model.ResponseFormat{Type: "json_schema", JSONSchema: nil}
	anthropicReq := AnthropicRequest{}
	adaptResponseFormatForAnthropic(rf, &anthropicReq)
	if !strings.Contains(anthropicReq.System, "JSON") {
		t.Errorf("missing JSON directive: %q", anthropicReq.System)
	}
}
