package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/model"
)

func withProviderHealthResolver(t *testing.T, resolver func(model.ProviderRoute) (bool, bool)) {
	t.Helper()
	previous := getProviderHealthResolver()
	SetProviderHealthResolver(resolver)
	t.Cleanup(func() {
		SetProviderHealthResolver(previous)
	})
}

func requestWithBodyContext(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestedModel, isStream, decodeErr := extractRequestBodyMeta([]byte(body))
	bodyCtx := &RequestBodyContext{
		RawBody:        []byte(body),
		UpstreamBody:   []byte(body),
		RequestedModel: requestedModel,
		IsStream:       isStream,
		DecodeError:    decodeErr,
	}
	ctx := context.WithValue(req.Context(), BodyContextKey, bodyCtx)
	return req.WithContext(ctx)
}

func providerConfig(name, model string) config.ProviderConfig {
	return config.ProviderConfig{
		Name:    name,
		BaseURL: "https://" + name + ".example.com",
		APIKey:  "sk-" + name,
		Models:  []string{model},
	}
}

func TestModelRouterMiddleware_MissingBodyContext(t *testing.T) {
	config.GlobalConfig = &config.Config{}

	handler := ModelRouterMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

func TestModelRouterMiddleware_InvalidJSON(t *testing.T) {
	config.GlobalConfig = &config.Config{}

	handler := ModelRouterMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := requestWithBodyContext(http.MethodPost, "/v1/chat/completions", "not-json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestModelRouterMiddleware_UsesBodyContextDecodeError(t *testing.T) {
	config.GlobalConfig = &config.Config{}

	handler := ModelRouterMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	bodyCtx := &RequestBodyContext{
		RawBody:        []byte(`{"model":"gpt-4o"}`),
		UpstreamBody:   []byte(`{"model":"gpt-4o"}`),
		RequestedModel: "gpt-4o",
		DecodeError:    errors.New("invalid json"),
	}
	req = req.WithContext(context.WithValue(req.Context(), BodyContextKey, bodyCtx))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestModelRouterMiddleware_MissingModel(t *testing.T) {
	config.GlobalConfig = &config.Config{}

	handler := ModelRouterMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := requestWithBodyContext(http.MethodPost, "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	var errEnv map[string]interface{}
	_ = json.NewDecoder(rr.Body).Decode(&errEnv)
	errObj, _ := errEnv["error"].(map[string]interface{})
	if code, _ := errObj["code"].(string); code != "missing_required_field" {
		t.Errorf("error code = %q, want %q", code, "missing_required_field")
	}
}

func TestModelRouterMiddleware_ModelNotFound(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("openai", "gpt-4o"),
		},
	}

	handler := ModelRouterMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := requestWithBodyContext(http.MethodPost, "/v1/chat/completions", `{"model":"unknown-model","messages":[]}`)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestModelRouterMiddleware_Success(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("openai", "gpt-4o"),
		},
	}

	var capturedCtx *model.GatewayContext
	handler := ModelRouterMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		capturedCtx, ok = GetGatewayContext(r)
		if !ok {
			t.Fatal("expected GatewayContext in request context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := requestWithBodyContext(http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if capturedCtx == nil {
		t.Fatal("GatewayContext was not injected")
	}
	if capturedCtx.TargetModel != "gpt-4o" {
		t.Errorf("TargetModel = %q, want %q", capturedCtx.TargetModel, "gpt-4o")
	}
	if capturedCtx.TargetProvider != "openai" {
		t.Errorf("TargetProvider = %q, want %q", capturedCtx.TargetProvider, "openai")
	}
	if capturedCtx.RequestedModel != "gpt-4o" {
		t.Errorf("RequestedModel = %q, want %q", capturedCtx.RequestedModel, "gpt-4o")
	}
	if capturedCtx.RouteSelectionPolicy != routePolicyConfiguredOrder {
		t.Errorf("RouteSelectionPolicy = %q, want %q", capturedCtx.RouteSelectionPolicy, routePolicyConfiguredOrder)
	}
	if got := len(capturedCtx.RouteCandidates); got != 1 {
		t.Fatalf("len(RouteCandidates) = %d, want 1", got)
	}
	if capturedCtx.RouteCandidates[0].Provider != "openai" {
		t.Errorf("RouteCandidates[0].Provider = %q, want %q", capturedCtx.RouteCandidates[0].Provider, "openai")
	}
}

func TestModelRouterMiddleware_PrefersHealthyProviders(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("openai", "gpt-4o"),
			providerConfig("anthropic", "gpt-4o"),
		},
	}
	withProviderHealthResolver(t, func(candidate model.ProviderRoute) (bool, bool) {
		switch candidate.Name {
		case "openai":
			return true, false
		case "anthropic":
			return true, true
		default:
			return false, true
		}
	})

	var capturedCtx *model.GatewayContext
	handler := ModelRouterMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx, _ = GetGatewayContext(r)
		w.WriteHeader(http.StatusOK)
	}))

	req := requestWithBodyContext(http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if capturedCtx == nil {
		t.Fatal("expected gateway context")
	}
	if capturedCtx.TargetProvider != "anthropic" {
		t.Fatalf("TargetProvider = %q, want anthropic", capturedCtx.TargetProvider)
	}
	if capturedCtx.RouteSelectionPolicy != routePolicyHealthyFirst {
		t.Fatalf("RouteSelectionPolicy = %q, want %q", capturedCtx.RouteSelectionPolicy, routePolicyHealthyFirst)
	}
	if got := len(capturedCtx.CandidateProviders); got != 2 {
		t.Fatalf("len(CandidateProviders) = %d, want 2", got)
	}
	if capturedCtx.CandidateProviders[0].Name != "anthropic" || capturedCtx.CandidateProviders[1].Name != "openai" {
		t.Fatalf("candidate order = [%s %s], want [anthropic openai]", capturedCtx.CandidateProviders[0].Name, capturedCtx.CandidateProviders[1].Name)
	}
	if capturedCtx.RouteCandidates[0].Provider != "anthropic" || capturedCtx.RouteCandidates[1].Provider != "openai" {
		t.Fatalf("route candidate order = [%s %s], want [anthropic openai]", capturedCtx.RouteCandidates[0].Provider, capturedCtx.RouteCandidates[1].Provider)
	}
}

func TestMatchProviders_PreservesConfiguredOrder(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("first", "gpt-4o"),
			providerConfig("second", "gpt-4o"),
			providerConfig("third", "gpt-4o"),
		},
	}

	candidates := matchProviders("gpt-4o")

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	if candidates[0].Name != "first" {
		t.Errorf("candidates[0] = %q, want %q", candidates[0].Name, "first")
	}
	if candidates[1].Name != "second" {
		t.Errorf("candidates[1] = %q, want %q", candidates[1].Name, "second")
	}
	if candidates[2].Name != "third" {
		t.Errorf("candidates[2] = %q, want %q", candidates[2].Name, "third")
	}
}

func TestMatchProviders_NoMatchReturnsEmpty(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("openai", "gpt-4o"),
		},
	}

	candidates := matchProviders("claude-3-opus")
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(candidates))
	}
}

func TestMatchProviders_ProviderWithMultipleModels(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			{
				Name:    "openai",
				BaseURL: "https://api.openai.com",
				APIKey:  "sk-openai",
				Models:  []string{"gpt-4o", "gpt-4-turbo", "gpt-3.5-turbo"},
			},
		},
	}

	for _, m := range []string{"gpt-4o", "gpt-4-turbo", "gpt-3.5-turbo"} {
		candidates := matchProviders(m)
		if len(candidates) != 1 {
			t.Errorf("model %q: expected 1 candidate, got %d", m, len(candidates))
		}
	}

	candidates := matchProviders("gpt-5")
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for unknown model, got %d", len(candidates))
	}
}

func TestMatchProviders_RebuildsWhenConfigChanges(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("openai", "gpt-4o"),
		},
	}

	first := matchProviders("gpt-4o")
	if len(first) != 1 || first[0].Name != "openai" {
		t.Fatalf("first match = %+v, want openai", first)
	}

	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("anthropic", "gpt-4o"),
		},
	}

	second := matchProviders("gpt-4o")
	if len(second) != 1 || second[0].Name != "anthropic" {
		t.Fatalf("second match = %+v, want anthropic", second)
	}
}

func TestMatchProviders_AllUnhealthyPreservesConfiguredOrder(t *testing.T) {
	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			providerConfig("openai", "gpt-4o"),
			providerConfig("anthropic", "gpt-4o"),
		},
	}
	withProviderHealthResolver(t, func(candidate model.ProviderRoute) (bool, bool) {
		return true, false
	})

	candidates := matchProviders("gpt-4o")
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if candidates[0].Name != "openai" || candidates[1].Name != "anthropic" {
		t.Fatalf("candidate order = [%s %s], want [openai anthropic]", candidates[0].Name, candidates[1].Name)
	}
}
