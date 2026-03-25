package adapter

import (
	"encoding/json"
	"strconv"

	"github.com/fxwio/strait/internal/model"
)

type anthropicSimpleMessage struct {
	role    string
	content string
}

func translateOpenAIToAnthropicSimple(req *model.ChatCompletionRequest) ([]byte, bool) {
	if len(req.Tools) != 0 {
		return nil, false
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != "" && req.ResponseFormat.Type != "text" {
		return nil, false
	}

	messages := make([]anthropicSimpleMessage, 0, len(req.Messages))
	system := ""
	for _, msg := range req.Messages {
		if len(msg.ToolCalls) != 0 || msg.ToolCallID != "" || msg.Name != "" {
			return nil, false
		}

		content, ok := rawMessageAsSimpleString(msg.Content)
		if !ok {
			return nil, false
		}

		switch msg.Role {
		case "system":
			system = content
		case "user", "assistant":
			messages = append(messages, anthropicSimpleMessage{
				role:    msg.Role,
				content: content,
			})
		default:
			return nil, false
		}
	}
	if len(messages) == 0 {
		return nil, false
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	buf := make([]byte, 0, estimateSimpleAnthropicRequestSize(req, system, messages))
	buf = append(buf, '{')
	buf = appendJSONStringField(buf, "model", req.Model)
	buf = append(buf, ',')
	buf = appendJSONIntField(buf, "max_tokens", int64(maxTokens))
	if req.Temperature != 0 {
		buf = append(buf, ',')
		buf = appendJSONFloat32Field(buf, "temperature", req.Temperature)
	}
	if req.Stream {
		buf = append(buf, ',')
		buf = appendJSONBoolField(buf, "stream", true)
	}
	if req.TopP != 0 {
		buf = append(buf, ',')
		buf = appendJSONFloat32Field(buf, "top_p", req.TopP)
	}
	if system != "" {
		buf = append(buf, ',')
		buf = appendJSONStringField(buf, "system", system)
	}
	if len(req.Stop) != 0 {
		buf = append(buf, ',')
		buf = appendJSONStringArrayField(buf, "stop_sequences", req.Stop)
	}

	buf = append(buf, `,"messages":[`...)
	for i, msg := range messages {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '{')
		buf = appendJSONStringField(buf, "role", msg.role)
		buf = append(buf, ',')
		buf = appendJSONStringField(buf, "content", msg.content)
		buf = append(buf, '}')
	}
	buf = append(buf, ']', '}')
	return buf, true
}

func rawMessageAsSimpleString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func estimateSimpleAnthropicRequestSize(req *model.ChatCompletionRequest, system string, messages []anthropicSimpleMessage) int {
	size := len(req.Model) + len(system) + 96
	for _, stop := range req.Stop {
		size += len(stop) + 4
	}
	for _, msg := range messages {
		size += len(msg.role) + len(msg.content) + 32
	}
	return size
}

func appendJSONFloat32Field(dst []byte, key string, value float32) []byte {
	dst = append(dst, '"')
	dst = append(dst, key...)
	dst = append(dst, `":`...)
	return strconv.AppendFloat(dst, float64(value), 'f', -1, 32)
}

func appendJSONBoolField(dst []byte, key string, value bool) []byte {
	dst = append(dst, '"')
	dst = append(dst, key...)
	dst = append(dst, `":`...)
	return strconv.AppendBool(dst, value)
}

func appendJSONStringArrayField(dst []byte, key string, values []string) []byte {
	dst = append(dst, '"')
	dst = append(dst, key...)
	dst = append(dst, `":[`...)
	for i, value := range values {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = strconv.AppendQuote(dst, value)
	}
	return append(dst, ']')
}
