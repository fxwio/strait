package adapter

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type anthropicToolUseResponse struct {
	ID         string                       `json:"id"`
	Model      string                       `json:"model"`
	StopReason string                       `json:"stop_reason"`
	Usage      AnthropicUsage               `json:"usage"`
	Error      *AnthropicError              `json:"error,omitempty"`
	Content    []anthropicToolUseContentRef `json:"content"`
}

type anthropicToolUseContentRef struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

func translateAnthropicToolUseToOpenAI(data []byte) ([]byte, bool, error) {
	if !bytes.Contains(data, []byte(`"tool_use"`)) {
		return nil, false, nil
	}

	var resp anthropicToolUseResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false, nil
	}
	if resp.Error != nil {
		return nil, false, nil
	}

	var (
		textLen   int
		toolCount int
	)
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textLen += len(block.Text)
		case "tool_use":
			toolCount++
		default:
			return nil, false, nil
		}
	}
	if toolCount == 0 {
		return nil, false, nil
	}

	var textBuilder strings.Builder
	if textLen > 0 {
		textBuilder.Grow(textLen)
		for _, block := range resp.Content {
			if block.Type == "text" {
				textBuilder.WriteString(block.Text)
			}
		}
	}

	finishReason := stopReasonToFinishReason(resp.StopReason)
	totalTokens := resp.Usage.InputTokens + resp.Usage.OutputTokens
	created := time.Now().Unix()

	out := make([]byte, 0, len(data)+192)
	out = append(out, '{')
	out = appendJSONStringField(out, "id", resp.ID)
	out = append(out, ',')
	out = appendJSONConstStringField(out, "object", "chat.completion")
	out = append(out, ',')
	out = appendJSONIntField(out, "created", created)
	out = append(out, ',')
	out = appendJSONStringField(out, "model", resp.Model)
	out = append(out, `,"choices":[{"index":0,"message":{"role":"assistant","content":`...)
	out = strconv.AppendQuote(out, textBuilder.String())
	out = append(out, `,"tool_calls":[`...)

	toolIndex := 0
	for _, block := range resp.Content {
		if block.Type != "tool_use" {
			continue
		}
		if toolIndex > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"id":`...)
		out = strconv.AppendQuote(out, block.ID)
		out = append(out, `,"type":"function","function":{"name":`...)
		out = strconv.AppendQuote(out, block.Name)
		out = append(out, `,"arguments":`...)
		out = appendQuotedJSONBytes(out, normalizeJSONRawMessage(block.Input))
		out = append(out, "}}"...)
		toolIndex++
	}

	out = append(out, `]},"finish_reason":`...)
	out = strconv.AppendQuote(out, finishReason)
	out = append(out, `}],"usage":{"prompt_tokens":`...)
	out = strconv.AppendInt(out, int64(resp.Usage.InputTokens), 10)
	out = append(out, `,"completion_tokens":`...)
	out = strconv.AppendInt(out, int64(resp.Usage.OutputTokens), 10)
	out = append(out, `,"total_tokens":`...)
	out = strconv.AppendInt(out, int64(totalTokens), 10)
	out = append(out, `}}`...)
	return out, true, nil
}

func normalizeJSONRawMessage(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}

func appendQuotedJSONBytes(dst []byte, raw []byte) []byte {
	const hex = "0123456789abcdef"

	dst = append(dst, '"')
	for _, b := range raw {
		switch b {
		case '\\', '"':
			dst = append(dst, '\\', b)
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if b < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hex[b>>4], hex[b&0x0f])
				continue
			}
			dst = append(dst, b)
		}
	}
	return append(dst, '"')
}
