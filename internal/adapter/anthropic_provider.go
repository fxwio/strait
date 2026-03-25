package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/pkg/logger"
)

type AnthropicProvider struct{}

func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

func (p *AnthropicProvider) CompileRequest(targetURL *url.URL, origPath string, origHeader http.Header, body []byte, apiKey string) (string, []byte, http.Header, error) {
	newBody, err := TranslateOpenAIToAnthropicBody(body)
	if err != nil {
		return "", nil, nil, err
	}
	header := origHeader
	header.Set("x-api-key", apiKey)
	header.Set("anthropic-version", "2023-06-01")
	header.Del("Authorization")
	// Anthropic always uses /v1/messages for chat completions.
	return "/v1/messages", newBody, header, nil
}

func (p *AnthropicProvider) GenerateProbeRequest(targetURL *url.URL, apiKey string, modelName string) (*http.Request, error) {
	u := *targetURL
	u.Path = "/v1/messages"

	payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":1}`, modelName)
	req, err := http.NewRequest(http.MethodPost, u.String(), strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	return req, nil
}

func (p *AnthropicProvider) TranslateResponse(resp *http.Response, w http.ResponseWriter, modelName string) error {
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return TranslateAnthropicStream(resp, w, modelName)
	}

	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB cap
	if err != nil {
		return fmt.Errorf("read anthropic response: %w", err)
	}

	// Pass through non-200 responses unchanged so error details reach the client
	if resp.StatusCode != http.StatusOK {
		copyResponseHeaders(w.Header(), resp.Header)
		w.Header().Del("Server")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(resp.StatusCode)
		_, err = w.Write(body)
		return err
	}

	translated, err := TranslateAnthropicToOpenAI(body)
	if err != nil {
		logger.Log.Warn("anthropic→openai translation failed, passing through raw",
			"error", err,
		)
		// Fall back to raw response rather than failing the request
		copyResponseHeaders(w.Header(), resp.Header)
		w.Header().Del("Server")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, err = w.Write(body)
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Server")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(translated)
	return err
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if shouldSkipResponseHeader(key) {
			continue
		}
		dst[key] = values
	}
}

// TranslateOpenAIToAnthropicBody converts an OpenAI-compatible chat completion
// request payload into Anthropic's native message payload.
func TranslateOpenAIToAnthropicBody(bodyBytes []byte) ([]byte, error) {
	if len(bodyBytes) == 0 {
		return nil, fmt.Errorf("empty request body")
	}

	var openAIReq model.ChatCompletionRequest
	if err := json.Unmarshal(bodyBytes, &openAIReq); err != nil {
		return nil, err
	}
	if body, ok := translateOpenAIToAnthropicSimple(&openAIReq); ok {
		return body, nil
	}

	anthropicReq := AnthropicRequest{
		Model:       openAIReq.Model,
		MaxTokens:   openAIReq.MaxTokens,
		Temperature: openAIReq.Temperature,
		Stream:      openAIReq.Stream,
		TopP:        openAIReq.TopP,
	}
	if len(openAIReq.Tools) > 0 {
		anthropicReq.Tools = make([]AnthropicTool, 0, len(openAIReq.Tools))
	}
	if len(openAIReq.Messages) > 0 {
		anthropicReq.Messages = make([]AnthropicMessage, 0, len(openAIReq.Messages))
	}

	if anthropicReq.MaxTokens == 0 {
		anthropicReq.MaxTokens = 4096
	}

	if len(openAIReq.Stop) > 0 {
		anthropicReq.StopSequences = openAIReq.Stop
	}

	for _, t := range openAIReq.Tools {
		anthropicReq.Tools = append(anthropicReq.Tools, AnthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	for _, msg := range openAIReq.Messages {
		switch msg.Role {
		case "system":
			anthropicReq.System = contentAsString(msg.Content)
		case "tool":
			anthropicReq.Messages = append(anthropicReq.Messages, translateToolResultMessage(msg))
		case "assistant":
			anthropicReq.Messages = append(anthropicReq.Messages, translateAssistantMessage(msg))
		default: // "user"
			anthropicReq.Messages = append(anthropicReq.Messages, translateUserMessage(msg))
		}
	}

	if openAIReq.ResponseFormat != nil {
		adaptResponseFormatForAnthropic(openAIReq.ResponseFormat, &anthropicReq)
	}
	if len(anthropicReq.Tools) == 0 {
		anthropicReq.Tools = nil
	}
	if len(anthropicReq.Messages) == 0 {
		anthropicReq.Messages = nil
	}

	return json.Marshal(anthropicReq)
}

func contentAsString(raw json.RawMessage) string {
	content, ok := decodeOpenAIMessageContent(raw)
	if !ok {
		return ""
	}
	return content.textOnly()
}

func translateUserMessage(msg model.RichMessage) AnthropicMessage {
	if len(msg.Content) == 0 {
		return AnthropicMessage{Role: "user", Content: ""}
	}

	content, ok := decodeOpenAIMessageContent(msg.Content)
	if !ok {
		return AnthropicMessage{Role: "user", Content: string(msg.Content)}
	}
	if content.kind == openAIMessageContentString {
		return AnthropicMessage{Role: "user", Content: content.text}
	}
	return AnthropicMessage{Role: "user", Content: content.anthropicBlocks()}
}

func translateAssistantMessage(msg model.RichMessage) AnthropicMessage {
	textContent := contentAsString(msg.Content)

	if len(msg.ToolCalls) == 0 {
		return AnthropicMessage{Role: "assistant", Content: textContent}
	}

	blocks := make([]AnthropicContentBlock, 0, len(msg.ToolCalls)+1)
	if textContent != "" {
		blocks = append(blocks, AnthropicContentBlock{
			Type: "text",
			Text: textContent,
		})
	}
	for _, tc := range msg.ToolCalls {
		blocks = append(blocks, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: toolCallArgumentsAsInput(tc.Function.Arguments),
		})
	}
	return AnthropicMessage{Role: "assistant", Content: blocks}
}

func translateToolResultMessage(msg model.RichMessage) AnthropicMessage {
	block := AnthropicContentBlock{
		Type:        "tool_result",
		ToolUseID:   msg.ToolCallID,
		ToolContent: contentAsString(msg.Content),
	}
	return AnthropicMessage{Role: "user", Content: []AnthropicContentBlock{block}}
}

func adaptResponseFormatForAnthropic(rf *model.ResponseFormat, anthropicReq *AnthropicRequest) {
	if rf == nil {
		return
	}

	const jsonObjectDirective = "\n\nIMPORTANT: You must respond with a valid JSON object only. " +
		"Do not include any explanation, markdown formatting, or text outside the JSON object."

	switch rf.Type {
	case "json_object":
		anthropicReq.System += jsonObjectDirective
	case "json_schema":
		if rf.JSONSchema == nil {
			anthropicReq.System += jsonObjectDirective
			return
		}
		schemaDesc := ""
		if rf.JSONSchema.Description != "" {
			schemaDesc = " (" + rf.JSONSchema.Description + ")"
		}
		var directive string
		if len(rf.JSONSchema.Schema) > 0 {
			schemaBytes, _ := json.MarshalIndent(json.RawMessage(rf.JSONSchema.Schema), "", "  ")
			directive = fmt.Sprintf(
				"\n\nIMPORTANT: Respond ONLY with a valid JSON object%s that strictly conforms "+
					"to the following JSON Schema. Do not include any explanation or text outside "+
					"the JSON object.\n\nSchema:\n```json\n%s\n```",
				schemaDesc,
				string(schemaBytes),
			)
		} else {
			directive = fmt.Sprintf(
				"\n\nIMPORTANT: Respond ONLY with a valid JSON object named %q%s. "+
					"Do not include any explanation or text outside the JSON object.",
				rf.JSONSchema.Name,
				schemaDesc,
			)
		}
		anthropicReq.System += directive
	}
}

// TranslateAnthropicToOpenAI converts a non-streaming Anthropic response body
// to the OpenAI ChatCompletionResponse format.
func TranslateAnthropicToOpenAI(data []byte) ([]byte, error) {
	if out, ok, err := translateAnthropicTextOnlyToOpenAI(data); ok || err != nil {
		return out, err
	}
	if out, ok, err := translateAnthropicToolUseToOpenAI(data); ok || err != nil {
		return out, err
	}

	var ar AnthropicResponse
	if err := json.Unmarshal(data, &ar); err != nil {
		return nil, fmt.Errorf("unmarshal anthropic response: %w", err)
	}

	if ar.Error != nil {
		return nil, fmt.Errorf("anthropic error %s: %s", ar.Error.Type, ar.Error.Message)
	}

	var textContent string
	var toolCalls []model.ToolCall

	for _, block := range ar.Content {
		switch block.Type {
		case "text":
			textContent += block.Text

		case "tool_use":
			argsBytes, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, model.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: model.ToolCallFunction{
					Name:      block.Name,
					Arguments: string(argsBytes),
				},
			})
		}
	}

	contentRaw, _ := json.Marshal(textContent)
	msg := model.RichMessage{
		Role:      "assistant",
		Content:   contentRaw,
		ToolCalls: toolCalls,
	}
	if len(toolCalls) == 0 {
		msg.ToolCalls = nil
	}

	finishReason := stopReasonToFinishReason(ar.StopReason)

	resp := model.ChatCompletionResponse{
		ID:      ar.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   ar.Model,
		Choices: []model.ChatCompletionChoice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
		Usage: model.Usage{
			PromptTokens:     ar.Usage.InputTokens,
			CompletionTokens: ar.Usage.OutputTokens,
			TotalTokens:      ar.Usage.InputTokens + ar.Usage.OutputTokens,
		},
	}

	return json.Marshal(resp)
}

// stopReasonToFinishReason maps Anthropic stop_reason to OpenAI finish_reason.
func stopReasonToFinishReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

// AnthropicRequest 代表 Anthropic (Claude) 原生的请求结构
type AnthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	MaxTokens     int                `json:"max_tokens"`
	Temperature   float32            `json:"temperature,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	System        string             `json:"system,omitempty"` // Claude 的 System prompt 是独立字段
	TopP          float32            `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
}

// AnthropicMessage is a message in the Anthropic API.
type AnthropicMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content any    `json:"content"` // string | []AnthropicContentBlock
}

// AnthropicContentBlock is one block in an Anthropic message content array.
type AnthropicContentBlock struct {
	Type string `json:"type"` // "text" | "image" | "tool_use" | "tool_result"

	// text block
	Text string `json:"text,omitempty"`

	// tool_use block (assistant requesting a tool call)
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"` // map[string]any

	// tool_result block (user providing a tool result)
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Content for tool_result can be a string or []AnthropicContentBlock
	ToolContent any `json:"content,omitempty"`

	// image block
	Source *AnthropicImageSource `json:"source,omitempty"`
}

// AnthropicImageSource describes an image in an Anthropic message.
type AnthropicImageSource struct {
	Type      string `json:"type"`       // "base64" | "url"
	MediaType string `json:"media_type"` // "image/jpeg" | "image/png" | ...
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// AnthropicResponse is the non-streaming response from the Anthropic API.
type AnthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"` // "message"
	Role       string                  `json:"role"` // "assistant"
	Model      string                  `json:"model"`
	Content    []AnthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"` // "end_turn" | "tool_use" | "max_tokens"
	Usage      AnthropicUsage          `json:"usage"`
	Error      *AnthropicError         `json:"error,omitempty"`
}

// AnthropicUsage tracks token usage in Anthropic API responses.
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// AnthropicError is the error object returned by the Anthropic API.
type AnthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// AnthropicTool represents a tool definition in the Anthropic API format.
type AnthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}
