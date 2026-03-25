package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fxwio/strait/internal/response"
)

// injectAllowlistAuth wraps a handler and injects a ClientAuthContext with the
// given allowedModels into the request context.
func injectAllowlistAuth(allowedModels []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCtx := &ClientAuthContext{
			Token:         "sk-test",
			Fingerprint:   "fp-test",
			TokenName:     "test-token",
			AllowedModels: allowedModels,
		}
		ctx := context.WithValue(r.Context(), ClientAuthContextKey, authCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// runAllowlist drives:
//
//	BodyContextMiddleware → injectAllowlistAuth → ModelAllowlistMiddleware → passthrough
func runAllowlist(t *testing.T, requestedModel string, allowedModels []string) *httptest.ResponseRecorder {
	t.Helper()
	passthrough := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})
	body := `{"model":"` + requestedModel + `","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	BodyContextMiddleware(DefaultMaxRequestBodyBytes,
		injectAllowlistAuth(allowedModels,
			ModelAllowlistMiddleware(passthrough),
		),
	).ServeHTTP(w, req)
	return w
}

func TestModelAllowlist_NoRestriction_Passes(t *testing.T) {
	w := runAllowlist(t, "gpt-4o", nil)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (nil allowlist)", w.Code)
	}
}

func TestModelAllowlist_ModelInList_Passes(t *testing.T) {
	w := runAllowlist(t, "gpt-4o", []string{"gpt-4o", "gpt-4o-mini"})
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestModelAllowlist_ModelNotInList_Returns403(t *testing.T) {
	w := runAllowlist(t, "gpt-4-turbo", []string{"gpt-4o", "gpt-4o-mini"})
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "gpt-4-turbo") {
		t.Errorf("error body should mention denied model, got: %s", w.Body.String())
	}

	var env response.OpenAIErrorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if env.Error.Type != response.ErrorTypePermission {
		t.Fatalf("error.type = %q, want %q", env.Error.Type, response.ErrorTypePermission)
	}
	if env.Error.Code == nil || *env.Error.Code != "model_not_allowed" {
		t.Fatalf("error.code = %v, want model_not_allowed", env.Error.Code)
	}
}

func TestModelAllowlist_CaseInsensitive(t *testing.T) {
	w := runAllowlist(t, "gpt-4o", []string{"GPT-4O"})
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (case-insensitive)", w.Code)
	}
}

func TestModelAllowlist_EmptySlice_Unrestricted(t *testing.T) {
	w := runAllowlist(t, "any-model", []string{})
	if w.Code != http.StatusOK {
		t.Errorf("empty slice should mean unrestricted, got %d", w.Code)
	}
}

func TestModelAllowlist_NoAuthContext_PassesThrough(t *testing.T) {
	passthrough := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	BodyContextMiddleware(DefaultMaxRequestBodyBytes, ModelAllowlistMiddleware(passthrough)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when no auth context", w.Code)
	}
}

// Unit tests for isModelAllowed.

func TestIsModelAllowed_InList(t *testing.T) {
	list := []string{"gpt-4o", "gpt-4o-mini", "claude-3-5-sonnet-20241022"}
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-4o", true},
		{"GPT-4O", true},
		{"gpt-4o-mini", true},
		{"gpt-4-turbo", false},
		{"", false},
		{"claude-3-5-sonnet-20241022", true},
	}
	for _, tc := range cases {
		got := isModelAllowed(tc.model, list)
		if got != tc.want {
			t.Errorf("isModelAllowed(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestIsModelAllowed_EmptyList(t *testing.T) {
	if isModelAllowed("gpt-4o", nil) {
		t.Error("nil list should return false, not grant access")
	}
	if isModelAllowed("gpt-4o", []string{}) {
		t.Error("empty list should return false")
	}
}
