package adapter

import (
	"encoding/json"
	"testing"

	"github.com/fxwio/strait/internal/model"
)

func TestTranslateAnthropicToOpenAI_TextOnly(t *testing.T) {
	input := `{
		"id": "msg_01XFDUDYJgAACzvnptvVoYEL",
		"type": "message",
		"role": "assistant",
		"model": "claude-3-5-sonnet-20241022",
		"content": [{"type": "text", "text": "Hello, how can I help?"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 15, "output_tokens": 8}
	}`

	out, err := TranslateAnthropicToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}

	// Content should be "Hello, how can I help?"
	var content string
	if err := json.Unmarshal(resp.Choices[0].Message.Content, &content); err != nil {
		t.Fatalf("unmarshal message content: %v", err)
	}
	if content != "Hello, how can I help?" {
		t.Errorf("content = %q, want %q", content, "Hello, how can I help?")
	}

	if resp.Usage.PromptTokens != 15 {
		t.Errorf("prompt_tokens = %d, want 15", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 8 {
		t.Errorf("completion_tokens = %d, want 8", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 23 {
		t.Errorf("total_tokens = %d, want 23", resp.Usage.TotalTokens)
	}
}

func TestTranslateAnthropicToOpenAI_MultipleTextBlocksAndEscapes(t *testing.T) {
	input := `{
		"id": "msg_multi_text",
		"type": "message",
		"role": "assistant",
		"model": "claude-3-5-sonnet-20241022",
		"content": [
			{"type": "text", "text": "Hello, "},
			{"type": "text", "text": "\"world\"\nnext"}
		],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 7, "output_tokens": 5}
	}`

	out, err := TranslateAnthropicToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	var content string
	if err := json.Unmarshal(resp.Choices[0].Message.Content, &content); err != nil {
		t.Fatalf("unmarshal message content: %v", err)
	}
	if content != "Hello, \"world\"\nnext" {
		t.Fatalf("content = %q, want %q", content, "Hello, \"world\"\nnext")
	}
}

func TestTranslateAnthropicToOpenAI_ToolUse(t *testing.T) {
	input := `{
		"id": "msg_tool_test",
		"type": "message",
		"role": "assistant",
		"model": "claude-3-5-sonnet-20241022",
		"content": [
			{"type": "text", "text": "I'll check the weather for you."},
			{
				"type": "tool_use",
				"id": "toolu_01A09q90qw90lq917835lq9",
				"name": "get_weather",
				"input": {"location": "San Francisco", "unit": "celsius"}
			}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 100, "output_tokens": 50}
	}`

	out, err := TranslateAnthropicToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}

	toolCalls := resp.Choices[0].Message.ToolCalls
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}

	tc := toolCalls[0]
	if tc.ID != "toolu_01A09q90qw90lq917835lq9" {
		t.Errorf("tool call id = %q", tc.ID)
	}
	if tc.Type != "function" {
		t.Errorf("tool call type = %q, want function", tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("function name = %q, want get_weather", tc.Function.Name)
	}

	// Arguments should be JSON string
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if args["location"] != "San Francisco" {
		t.Errorf("location = %q, want San Francisco", args["location"])
	}
}

func TestTranslateAnthropicToOpenAI_MultipleToolCalls(t *testing.T) {
	input := `{
		"id": "msg_multi",
		"type": "message",
		"role": "assistant",
		"model": "claude-3-5-sonnet-20241022",
		"content": [
			{"type": "tool_use", "id": "tool_1", "name": "search", "input": {"query": "Go lang"}},
			{"type": "tool_use", "id": "tool_2", "name": "search", "input": {"query": "Python"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 50, "output_tokens": 30}
	}`

	out, err := TranslateAnthropicToOpenAI([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(resp.Choices[0].Message.ToolCalls))
	}
}

func TestTranslateAnthropicToOpenAI_StopReasonMapping(t *testing.T) {
	tests := []struct {
		stopReason string
		wantFinish string
	}{
		{"end_turn", "stop"},
		{"tool_use", "tool_calls"},
		{"max_tokens", "length"},
		{"stop_sequence", "stop"},
		{"", "stop"},
	}

	for _, tt := range tests {
		input := `{"id":"x","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"hi"}],"stop_reason":"` + tt.stopReason + `","usage":{"input_tokens":1,"output_tokens":1}}`
		out, err := TranslateAnthropicToOpenAI([]byte(input))
		if err != nil {
			t.Fatalf("stop_reason=%q error: %v", tt.stopReason, err)
		}
		var resp model.ChatCompletionResponse
		_ = json.Unmarshal(out, &resp)
		if resp.Choices[0].FinishReason != tt.wantFinish {
			t.Errorf("stop_reason=%q: finish_reason=%q, want %q",
				tt.stopReason, resp.Choices[0].FinishReason, tt.wantFinish)
		}
	}
}

func TestTranslateAnthropicToOpenAI_InvalidJSON(t *testing.T) {
	_, err := TranslateAnthropicToOpenAI([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestTranslateOpenAIToAnthropic_ToolCallsInAssistantMessage(t *testing.T) {
	payload := `{
		"model": "claude-3-5-sonnet-20241022",
		"messages": [
			{"role": "user", "content": "What's the weather?"},
			{
				"role": "assistant",
				"content": "I'll check.",
				"tool_calls": [{
					"id": "toolu_abc",
					"type": "function",
					"function": {"name": "get_weather", "arguments": "{\"city\":\"NYC\"}"}
				}]
			},
			{
				"role": "tool",
				"tool_call_id": "toolu_abc",
				"content": "72°F and sunny"
			}
		],
		"tools": [{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "Get current weather",
				"parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
			}
		}]
	}`
	out := translateAnthropicRequest(t, payload)

	// Should have 3 messages: user, assistant, user(tool_result)
	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out.Messages))
	}

	// Last message should be user with tool_result
	last := out.Messages[2]
	if last.Role != "user" {
		t.Errorf("last message role = %q, want user", last.Role)
	}

	// Verify assistant message has tool_use content block
	assistantMsg := out.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("assistant message role = %q", assistantMsg.Role)
	}
	blocks, ok := assistantMsg.Content.([]interface{})
	if !ok {
		t.Fatalf("assistant content should be array, got %T", assistantMsg.Content)
	}
	if len(blocks) == 0 {
		t.Error("expected assistant content blocks")
	}

	// Tools should be translated
	if len(out.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(out.Tools))
	}
	if out.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", out.Tools[0].Name)
	}
}

func TestTranslateOpenAIToAnthropic_InvalidToolArgumentsRemainString(t *testing.T) {
	payload := `{
		"model": "claude-3-5-sonnet-20241022",
		"messages": [{
			"role": "assistant",
			"content": "calling tool",
			"tool_calls": [{
				"id": "toolu_bad",
				"type": "function",
				"function": {"name": "get_weather", "arguments": "{bad json"}
			}]
		}]
	}`
	out := translateAnthropicRequest(t, payload)

	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out.Messages))
	}

	blocks, ok := out.Messages[0].Content.([]interface{})
	if !ok || len(blocks) != 2 {
		t.Fatalf("assistant content = %#v, want 2 blocks", out.Messages[0].Content)
	}

	toolBlock, ok := blocks[1].(map[string]interface{})
	if !ok {
		t.Fatalf("tool block = %#v", blocks[1])
	}
	if got := toolBlock["input"]; got != "{bad json" {
		t.Fatalf("tool input = %#v, want invalid JSON preserved as string", got)
	}
}

func TestTranslateOpenAIToAnthropic_VisionContent(t *testing.T) {
	payload := `{
		"model": "claude-3-5-sonnet-20241022",
		"messages": [{
			"role": "user",
			"content": [
				{"type": "text", "text": "What's in this image?"},
				{"type": "image_url", "image_url": {"url": "https://example.com/img.png"}}
			]
		}]
	}`
	out := translateAnthropicRequest(t, payload)

	if len(out.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out.Messages))
	}

	// Content should be an array of blocks ([]interface{} after JSON round-trip)
	blocks, ok := out.Messages[0].Content.([]interface{})
	if !ok {
		t.Fatalf("expected content array, got %T", out.Messages[0].Content)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(blocks))
	}
}
