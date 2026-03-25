package model

import (
	"encoding/json"
)

type ChatCompletionRequest struct {
	Model            string          `json:"model"`
	Messages         []RichMessage   `json:"messages"`
	Temperature      float32         `json:"temperature,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	MaxTokens        int             `json:"max_tokens,omitempty"`
	TopP             float32         `json:"top_p,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	PresencePenalty  float32         `json:"presence_penalty,omitempty"`
	FrequencyPenalty float32         `json:"frequency_penalty,omitempty"`
	Tools            []Tool          `json:"tools,omitempty"`
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat controls the output format of the model response.
// OpenAI supports "text", "json_object", and "json_schema" types.
// Anthropic does not natively support this field; the gateway adapts it by
// injecting JSON instructions into the system prompt during protocol translation.
type ResponseFormat struct {
	// Type is one of: "text" | "json_object" | "json_schema"
	Type string `json:"type"`
	// JSONSchema is only populated when Type == "json_schema".
	JSONSchema *JSONSchemaFormat `json:"json_schema,omitempty"`
}

// JSONSchemaFormat is the nested object for response_format.type == "json_schema".
type JSONSchemaFormat struct {
	// Name is a human-readable identifier for the schema (required by OpenAI).
	Name string `json:"name"`
	// Description is an optional human-readable description.
	Description string `json:"description,omitempty"`
	// Schema is the JSON Schema that the response must conform to.
	Schema json.RawMessage `json:"schema,omitempty"`
	// Strict, when true, instructs the model to strictly follow the schema.
	Strict bool `json:"strict,omitempty"`
}

// Message is an OpenAI chat message (text-only variant).
// For multi-part content (vision, tool results), see RichMessage.
type Message struct {
	Role    string `json:"role"` // system, user, assistant, tool
	Content string `json:"content"`
}

// RichMessage is the full OpenAI message type supporting multi-part content,
// tool calls, and tool results. The translator uses this internally when
// parsing incoming requests before forwarding to upstream providers.
type RichMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"` // string | []ContentPart
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

// ContentPart is one element in a multi-part message content array.
type ContentPart struct {
	Type     string    `json:"type"` // "text" | "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL holds the URL for an image content part.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto" | "low" | "high"
}

// ToolCall is an assistant's request to call a tool.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name and JSON-encoded arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded string
}

// ChatCompletionChoice is one completion choice in a non-streaming OpenAI response.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      RichMessage `json:"message"`
	FinishReason string      `json:"finish_reason"` // "stop" | "tool_calls" | "length"
}

// ChatCompletionResponse is the full non-streaming OpenAI response format.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"` // "chat.completion"
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   Usage                  `json:"usage"`
}

// Tool represents an OpenAI function/tool definition.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction holds the function name, description, and parameter schema.
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ProviderRoute struct {
	Name            string
	BaseURL         string
	APIKey          string
	Priority        int
	MaxRetries      int
	HealthCheckPath string
	// TimeoutNonStream is the per-attempt deadline for non-streaming requests.
	// Empty string means "use the global default".
	TimeoutNonStream string
	// TimeoutStream is the per-attempt deadline for streaming (SSE) requests.
	// Empty string means "use the global default".
	TimeoutStream string
}

// GatewayContext 是贯穿网关中间件链的核心结构。
// 在 M2 中它不仅保存当前命中的 provider，还保存整个 failover 候选集。
type GatewayContext struct {
	RequestedModel       string
	TargetProvider       string
	TargetModel          string
	APIKey               string
	BaseURL              string
	CandidateProviders   []ProviderRoute
	RouteSelectionPolicy string
	RouteCandidates      RouteCandidateTraceList
	AttemptedProviders   []string
	UpstreamAttempts     UpstreamAttemptTraceList
	FailoverEvents       UpstreamFailoverTraceList
	FailoverCount        int
	StreamOutcome        string
	StreamChunks         int
	StreamBytes          int64
	FinalStatusCode      int
	FinalErrorType       string
	FinalErrorCode       string
	FinalFailureReason   string
}

func (g *GatewayContext) SetActiveProvider(provider ProviderRoute) {
	g.TargetProvider = provider.Name
	g.APIKey = provider.APIKey
	g.BaseURL = provider.BaseURL
	g.noteProviderAttempt(provider.Name)
}

func (g *GatewayContext) RecordUpstreamAttempt(trace UpstreamAttemptTrace) {
	g.UpstreamAttempts = append(g.UpstreamAttempts, trace)
}

func (g *GatewayContext) RecordFailover(trace UpstreamFailoverTrace) {
	g.FailoverEvents = append(g.FailoverEvents, trace)
}

func (g *GatewayContext) SetTerminalError(status int, errorType string, errorCode string, failureReason string) {
	g.FinalStatusCode = status
	g.FinalErrorType = errorType
	g.FinalErrorCode = errorCode
	g.FinalFailureReason = failureReason
}

func (g *GatewayContext) noteProviderAttempt(provider string) {
	if provider == "" {
		return
	}
	if n := len(g.AttemptedProviders); n > 0 && g.AttemptedProviders[n-1] == provider {
		return
	}
	g.AttemptedProviders = append(g.AttemptedProviders, provider)
}

type UpstreamAttemptTrace struct {
	Provider      string `json:"provider"`
	ProviderIndex int    `json:"provider_index"`
	Attempt       int    `json:"attempt"`
	AttemptBudget int    `json:"attempt_budget"`
	StatusCode    int    `json:"status_code,omitempty"`
	Result        string `json:"result"`
	Reason        string `json:"reason,omitempty"`
	DurationMs    int64  `json:"duration_ms"`
}

type UpstreamAttemptTraceList []UpstreamAttemptTrace

type UpstreamFailoverTrace struct {
	FromProvider  string `json:"from_provider"`
	ToProvider    string `json:"to_provider"`
	ProviderIndex int    `json:"provider_index"`
	FailoverCount int    `json:"failover_count"`
	Reason        string `json:"reason,omitempty"`
}

type UpstreamFailoverTraceList []UpstreamFailoverTrace

type RouteCandidateTrace struct {
	Provider string `json:"provider"`
	Priority int    `json:"priority"`
}

type RouteCandidateTraceList []RouteCandidateTrace

// OpenAIResponse 代表标准的大模型非流式返回结果。
type OpenAIResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// Usage 计费字段。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
