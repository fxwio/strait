//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/response"
	"github.com/fxwio/strait/internal/router"
	"github.com/fxwio/strait/pkg/logger"
)

// ── Tokens & secrets used throughout the suite ─────────────────────────────

const (
	metricsToken      = "metrics-secret-integration"
	tokenUnrestricted = "sk-integration-unrestricted-0000"
	tokenRestricted   = "sk-integration-restricted-gpt4o" // only gpt-4o allowed
	providerKey       = "sk-mock-provider-key"
)

// ── Package-level servers shared by all tests ──────────────────────────────

var (
	gw   *httptest.Server // gateway under test
	mock *httptest.Server // fake upstream (OpenAI-compatible)
)

// ── TestMain: start mock, load config, boot gateway ───────────────────────

func TestMain(m *testing.M) {
	mock = httptest.NewServer(http.HandlerFunc(mockUpstreamHandler))
	defer mock.Close()

	envVars := map[string]string{
		"INTEGRATION_METRICS_TOKEN":      metricsToken,
		"INTEGRATION_TOKEN_UNRESTRICTED": tokenUnrestricted,
		"INTEGRATION_TOKEN_RESTRICTED":   tokenRestricted,
		"INTEGRATION_PROVIDER_KEY":       providerKey,
	}
	for k, v := range envVars {
		os.Setenv(k, v) //nolint:errcheck
	}
	defer func() {
		for k := range envVars {
			os.Unsetenv(k) //nolint:errcheck
		}
	}()

	cfgContent := buildConfig(mock.URL)
	cfgFile, err := os.CreateTemp("", "gw-integration-*.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp config: %v\n", err)
		os.Exit(1)
	}
	if _, err = cfgFile.WriteString(cfgContent); err != nil {
		fmt.Fprintf(os.Stderr, "write temp config: %v\n", err)
		os.Exit(1)
	}
	cfgFile.Close()
	defer os.Remove(cfgFile.Name())

	logger.InitLogger()
	defer logger.Sync()

	if err := config.LoadConfig(cfgFile.Name()); err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	gw = httptest.NewServer(router.NewRouter())
	defer gw.Close()

	os.Exit(m.Run())
}

// ── Mock upstream ──────────────────────────────────────────────────────────

// mockUpstreamHandler returns a minimal OpenAI-compatible chat completion
// response. The returned model echoes the model in the request body so tests
// can verify routing preserved the requested model.
func mockUpstreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.NotFound(w, r)
		return
	}

	var body struct {
		Model string `json:"model"`
	}
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	model := body.Model
	if model == "" {
		model = "unknown"
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{
		"id":"chatcmpl-mock","object":"chat.completion","created":1700000000,
		"model":%q,
		"choices":[{"index":0,"message":{"role":"assistant","content":"mock response"},
		"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`, model)
}

// ── Config builder ────────────────────────────────────────────────────────

func buildConfig(mockURL string) string {
	return fmt.Sprintf(`
server:
  host: "127.0.0.1"
  port: 19090
  read_timeout: 5s
  read_header_timeout: 5s
  write_timeout: 5s
  idle_timeout: 10s
  shutdown_timeout: 2s
  trusted_proxy_cidrs:
    - "127.0.0.1/32"

metrics:
  bearer_token_env: "INTEGRATION_METRICS_TOKEN"
  allowed_cidrs: []

auth:
  rate_limit_qps: 100
  rate_limit_burst: 200
  tokens:
    - name: unrestricted
      tenant: test
      app: integration
      value_env: INTEGRATION_TOKEN_UNRESTRICTED
      rate_limit_qps: 100
      rate_limit_burst: 200

    - name: restricted-gpt4o
      tenant: test
      app: integration
      value_env: INTEGRATION_TOKEN_RESTRICTED
      rate_limit_qps: 100
      rate_limit_burst: 200
      allowed_models:
        - gpt-4o

providers:
  - name: "mock-openai"
    base_url: "%s"
    api_key_env: "INTEGRATION_PROVIDER_KEY"
    models:
      - "gpt-4o"
      - "gpt-4o-mini"
`, mockURL)
}

// ── Helper ────────────────────────────────────────────────────────────────

func do(t *testing.T, method, path, auth, body string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, gw.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

func chatBody(model string) string {
	return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, model)
}

func assertCode(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		t.Errorf("status %d, want %d — body: %s", resp.StatusCode, want, b)
	}
}

func assertCodeAndBody(t *testing.T, resp *http.Response, wantCode int, wantBodyContains string) {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		t.Errorf("status %d, want %d — body: %s", resp.StatusCode, wantCode, b)
	}
	if wantBodyContains != "" && !bytes.Contains(b, []byte(wantBodyContains)) {
		t.Errorf("body %q does not contain %q", b, wantBodyContains)
	}
}

func assertOpenAIError(t *testing.T, resp *http.Response, wantCode int, wantType string, wantErrCode string, wantBodyContains string) {
	t.Helper()
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		t.Fatalf("status %d, want %d — body: %s", resp.StatusCode, wantCode, body)
	}
	if wantBodyContains != "" && !bytes.Contains(body, []byte(wantBodyContains)) {
		t.Fatalf("body %q does not contain %q", body, wantBodyContains)
	}

	var env response.OpenAIErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode OpenAI error: %v; body: %s", err, body)
	}
	if env.Error.Type != wantType {
		t.Fatalf("error.type = %q, want %q", env.Error.Type, wantType)
	}
	if env.Error.Code == nil || *env.Error.Code != wantErrCode {
		t.Fatalf("error.code = %v, want %q", env.Error.Code, wantErrCode)
	}
}

// ── Tests: health & ops ───────────────────────────────────────────────────

func TestHealth_Live(t *testing.T) {
	assertCode(t, do(t, http.MethodGet, "/health/live", "", ""), http.StatusOK)
}

func TestHealth_Ready(t *testing.T) {
	resp := do(t, http.MethodGet, "/health/ready", "", "")
	defer resp.Body.Close()
	// Accepts 200 (ok / degraded) or 503 if all providers report unhealthy circuit.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unexpected status %d", resp.StatusCode)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if status, ok := payload["status"].(string); !ok || status == "" {
		t.Fatalf("expected status string, got %#v", payload["status"])
	}
}

// ── Tests: metrics endpoint ───────────────────────────────────────────────

func TestMetrics_NoToken_Forbidden(t *testing.T) {
	assertCode(t, do(t, http.MethodGet, "/metrics", "", ""), http.StatusForbidden)
}

func TestMetrics_WrongToken_Forbidden(t *testing.T) {
	assertCode(t, do(t, http.MethodGet, "/metrics", "wrong-token", ""), http.StatusForbidden)
}

func TestMetrics_ValidToken_OK(t *testing.T) {
	assertCode(t, do(t, http.MethodGet, "/metrics", metricsToken, ""), http.StatusOK)
}

// ── Tests: chat auth middleware ───────────────────────────────────────────

func TestChat_NoAuthHeader_401(t *testing.T) {
	assertOpenAIError(t,
		do(t, http.MethodPost, "/v1/chat/completions", "", chatBody("gpt-4o")),
		http.StatusUnauthorized, response.ErrorTypeAuthentication, "missing_authorization_header", "Missing Authorization")
}

func TestChat_InvalidToken_401(t *testing.T) {
	assertOpenAIError(t,
		do(t, http.MethodPost, "/v1/chat/completions", "sk-not-registered", chatBody("gpt-4o")),
		http.StatusUnauthorized, response.ErrorTypeAuthentication, "invalid_api_key", "invalid_api_key")
}

// ── Tests: model allowlist enforcement ───────────────────────────────────

func TestModelAllowlist_AllowedModel_200(t *testing.T) {
	// restricted token allows only gpt-4o
	resp := do(t, http.MethodPost, "/v1/chat/completions", tokenRestricted, chatBody("gpt-4o"))
	assertCode(t, resp, http.StatusOK)
}

func TestModelAllowlist_DeniedModel_403(t *testing.T) {
	// restricted token requesting gpt-4o-mini → 403
	assertOpenAIError(t,
		do(t, http.MethodPost, "/v1/chat/completions", tokenRestricted, chatBody("gpt-4o-mini")),
		http.StatusForbidden, response.ErrorTypePermission, "model_not_allowed", "gpt-4o-mini")
}

func TestModelAllowlist_Unrestricted_AnyModel_200(t *testing.T) {
	resp := do(t, http.MethodPost, "/v1/chat/completions", tokenUnrestricted, chatBody("gpt-4o-mini"))
	assertCode(t, resp, http.StatusOK)
}
