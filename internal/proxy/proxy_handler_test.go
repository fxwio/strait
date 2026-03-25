package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxwio/strait/internal/adapter"
	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/middleware"
	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/internal/response"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGatewayProxyHandler_FailsOverOnProviderAuthFailure(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	config.GlobalConfig = &config.Config{
		Upstream: config.UpstreamConfig{
			RetryableStatusCodes:    []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
			DefaultTimeoutNonStream: "1s",
			DefaultTimeoutStream:    "5s",
		},
	}

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{}
	providerHealthMu.Unlock()

	handler := &gatewayProxyHandler{
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Host {
				case "bad.example.com":
					return newJSONResponse(http.StatusUnauthorized, `{"error":"invalid upstream key"}`), nil
				case "good.example.com":
					return newJSONResponse(http.StatusOK, `{"id":"chatcmpl-test","object":"chat.completion","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), nil
				default:
					t.Fatalf("unexpected upstream host %q", req.URL.Host)
					return nil, nil
				}
			}),
		},
	}

	gatewayCtx := &model.GatewayContext{
		RequestedModel: "gpt-4o",
		TargetModel:    "gpt-4o",
		CandidateProviders: []model.ProviderRoute{
			{Name: "bad", BaseURL: "https://bad.example.com", APIKey: "sk-bad"},
			{Name: "good", BaseURL: "https://good.example.com", APIKey: "sk-good"},
		},
	}
	gatewayCtx.SetActiveProvider(gatewayCtx.CandidateProviders[0])

	req := newProxyTestRequest(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, gatewayCtx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Gateway-Upstream-Provider"); got != "good" {
		t.Fatalf("X-Gateway-Upstream-Provider = %q, want %q", got, "good")
	}
	if gatewayCtx.FailoverCount != 1 {
		t.Fatalf("FailoverCount = %d, want 1", gatewayCtx.FailoverCount)
	}
	if got := len(gatewayCtx.FailoverEvents); got != 1 {
		t.Fatalf("len(FailoverEvents) = %d, want 1", got)
	}
	if gatewayCtx.FailoverEvents[0].FromProvider != "bad" || gatewayCtx.FailoverEvents[0].ToProvider != "good" {
		t.Fatalf("FailoverEvents[0] = %+v, want bad -> good", gatewayCtx.FailoverEvents[0])
	}
	if got := len(gatewayCtx.UpstreamAttempts); got != 2 {
		t.Fatalf("len(UpstreamAttempts) = %d, want 2", got)
	}
	first := gatewayCtx.UpstreamAttempts[0]
	if first.Provider != "bad" || first.StatusCode != http.StatusUnauthorized || first.Result != "failover" {
		t.Fatalf("first attempt = %+v, want bad 401 failover", first)
	}
	second := gatewayCtx.UpstreamAttempts[1]
	if second.Provider != "good" || second.StatusCode != http.StatusOK || second.Result != "returned_response" {
		t.Fatalf("second attempt = %+v, want good 200 returned_response", second)
	}

	statuses := GetUpstreamStatuses()
	if status, ok := statuses["bad"]; !ok || status.Healthy {
		t.Fatalf("bad provider status = %+v, want unhealthy", status)
	}
}

func TestGatewayProxyHandler_FailsOverOnProviderRateLimit(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	config.GlobalConfig = &config.Config{
		Upstream: config.UpstreamConfig{
			RetryableStatusCodes:    []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
			DefaultTimeoutNonStream: "1s",
			DefaultTimeoutStream:    "5s",
		},
	}

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{}
	providerHealthMu.Unlock()

	var badAttempts int
	handler := &gatewayProxyHandler{
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Host {
				case "rate-limited.example.com":
					badAttempts++
					return newJSONResponse(http.StatusTooManyRequests, `{"error":"provider rate limit"}`), nil
				case "good.example.com":
					return newJSONResponse(http.StatusOK, `{"id":"chatcmpl-test","object":"chat.completion","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), nil
				default:
					t.Fatalf("unexpected upstream host %q", req.URL.Host)
					return nil, nil
				}
			}),
		},
	}

	gatewayCtx := &model.GatewayContext{
		RequestedModel: "gpt-4o",
		TargetModel:    "gpt-4o",
		CandidateProviders: []model.ProviderRoute{
			{Name: "rate-limited", BaseURL: "https://rate-limited.example.com", APIKey: "sk-bad", MaxRetries: 3},
			{Name: "good", BaseURL: "https://good.example.com", APIKey: "sk-good"},
		},
	}
	gatewayCtx.SetActiveProvider(gatewayCtx.CandidateProviders[0])

	req := newProxyTestRequest(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, gatewayCtx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if badAttempts != 1 {
		t.Fatalf("rate-limited provider attempts = %d, want 1", badAttempts)
	}
	if got := rr.Header().Get("X-Gateway-Upstream-Provider"); got != "good" {
		t.Fatalf("X-Gateway-Upstream-Provider = %q, want %q", got, "good")
	}
	if got := rr.Header().Get("X-Gateway-Upstream-Retries"); got != "0" {
		t.Fatalf("X-Gateway-Upstream-Retries = %q, want 0", got)
	}
	if gatewayCtx.FailoverCount != 1 {
		t.Fatalf("FailoverCount = %d, want 1", gatewayCtx.FailoverCount)
	}
	if got := len(gatewayCtx.UpstreamAttempts); got != 2 {
		t.Fatalf("len(UpstreamAttempts) = %d, want 2", got)
	}
	first := gatewayCtx.UpstreamAttempts[0]
	if first.Provider != "rate-limited" || first.StatusCode != http.StatusTooManyRequests || first.Result != "failover" || first.Reason != "status_429" {
		t.Fatalf("first attempt = %+v, want rate-limited 429 failover", first)
	}
	if status, ok := GetUpstreamStatuses()["rate-limited"]; !ok || status.Healthy {
		t.Fatalf("rate-limited provider status = %+v, want unhealthy", status)
	}
}

func TestGatewayProxyHandler_LastProviderFailureDoesNotRecordFailoverToNone(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	config.GlobalConfig = &config.Config{
		Upstream: config.UpstreamConfig{
			RetryableStatusCodes:    []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
			DefaultTimeoutNonStream: "1s",
			DefaultTimeoutStream:    "5s",
		},
	}

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{}
	providerHealthMu.Unlock()

	handler := &gatewayProxyHandler{
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return newJSONResponse(http.StatusUnauthorized, `{"error":"invalid upstream key"}`), nil
			}),
		},
	}

	gatewayCtx := &model.GatewayContext{
		RequestedModel: "gpt-4o",
		TargetModel:    "gpt-4o",
		CandidateProviders: []model.ProviderRoute{
			{Name: "bad", BaseURL: "https://bad.example.com", APIKey: "sk-bad"},
		},
	}
	gatewayCtx.SetActiveProvider(gatewayCtx.CandidateProviders[0])

	req := newProxyTestRequest(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, gatewayCtx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	if gatewayCtx.FailoverCount != 0 {
		t.Fatalf("FailoverCount = %d, want 0", gatewayCtx.FailoverCount)
	}
	if got := len(gatewayCtx.FailoverEvents); got != 0 {
		t.Fatalf("len(FailoverEvents) = %d, want 0", got)
	}
	if gatewayCtx.FinalErrorCode != "all_upstreams_unavailable" {
		t.Fatalf("FinalErrorCode = %q, want all_upstreams_unavailable", gatewayCtx.FinalErrorCode)
	}

	var env response.OpenAIErrorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if env.Error.Code == nil || *env.Error.Code != "all_upstreams_unavailable" {
		t.Fatalf("error.code = %v, want all_upstreams_unavailable", env.Error.Code)
	}
}

func TestRequestTemplateAnthropicCompile_DoesNotMutateOriginalHeaders(t *testing.T) {
	rawBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://gateway/v1/chat/completions", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Custom", "keep-me")

	template := newRequestTemplate(req, rawBody)
	p := model.ProviderRoute{
		Name:    "anthropic",
		BaseURL: "https://api.anthropic.com",
		APIKey:  "sk-anthropic",
	}
	compiled, err := template.compile(p, adapter.GetProvider(p.Name))
	if err != nil {
		t.Fatalf("compile anthropic request: %v", err)
	}

	upstreamReq := template.newRequest(context.Background(), compiled)

	if got := req.Header.Get("Authorization"); got != "Bearer client-token" {
		t.Fatalf("original Authorization = %q, want client token preserved", got)
	}
	if got := req.Header.Get("Accept-Encoding"); got != "gzip" {
		t.Fatalf("original Accept-Encoding = %q, want gzip preserved", got)
	}
	if got := req.Header.Get("X-Custom"); got != "keep-me" {
		t.Fatalf("original X-Custom = %q, want keep-me", got)
	}

	if got := upstreamReq.Header.Get("Authorization"); got != "" {
		t.Fatalf("anthropic Authorization = %q, want empty", got)
	}
	if got := upstreamReq.Header.Get("x-api-key"); got != "sk-anthropic" {
		t.Fatalf("anthropic x-api-key = %q, want sk-anthropic", got)
	}
	if got := upstreamReq.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
	}
	if got := upstreamReq.Header.Get("Accept-Encoding"); got != "" {
		t.Fatalf("upstream Accept-Encoding = %q, want empty", got)
	}
	if got := upstreamReq.Header.Get("X-Custom"); got != "keep-me" {
		t.Fatalf("upstream X-Custom = %q, want keep-me", got)
	}
	if got := upstreamReq.URL.String(); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("upstream URL = %q, want %q", got, "https://api.anthropic.com/v1/messages")
	}
	if got := upstreamReq.Host; got != "api.anthropic.com" {
		t.Fatalf("upstream Host = %q, want %q", got, "api.anthropic.com")
	}
	if upstreamReq.ContentLength != int64(len(compiled.body)) {
		t.Fatalf("ContentLength = %d, want %d", upstreamReq.ContentLength, len(compiled.body))
	}
	if got := upstreamReq.Header.Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length header = %q, want empty", got)
	}
}

func newProxyTestRequest(body string, gatewayCtx *model.GatewayContext) *http.Request {
	rawBody := []byte(body)
	req := httptest.NewRequest(http.MethodPost, "http://gateway/v1/chat/completions", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")

	bodyCtx := &middleware.RequestBodyContext{
		RawBody:        rawBody,
		UpstreamBody:   rawBody,
		RequestedModel: gatewayCtx.TargetModel,
	}

	ctx := context.WithValue(req.Context(), middleware.BodyContextKey, bodyCtx)
	ctx = context.WithValue(ctx, middleware.GatewayContextKey, gatewayCtx)
	return req.WithContext(ctx)
}

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewBufferString(body)),
	}
}
