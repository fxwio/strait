package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPtr(t *testing.T) {
	t.Run("non-empty string returns pointer", func(t *testing.T) {
		s := "hello"
		p := Ptr(s)
		if p == nil {
			t.Fatal("expected non-nil pointer")
		}
		if *p != s {
			t.Errorf("got %q, want %q", *p, s)
		}
	})

	t.Run("empty string returns nil", func(t *testing.T) {
		if p := Ptr(""); p != nil {
			t.Errorf("expected nil for empty string, got %v", p)
		}
	})
}

func TestDefaultErrorType(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, ErrorTypeAuthentication},
		{http.StatusForbidden, ErrorTypePermission},
		{http.StatusTooManyRequests, ErrorTypeRateLimit},
		{http.StatusBadRequest, ErrorTypeInvalidRequest},
		{http.StatusNotFound, ErrorTypeInvalidRequest},
		{http.StatusRequestEntityTooLarge, ErrorTypeInvalidRequest},
		{http.StatusInternalServerError, ErrorTypeServer},
		{http.StatusServiceUnavailable, ErrorTypeServer},
	}

	for _, tt := range tests {
		got := defaultErrorType(tt.status)
		if got != tt.want {
			t.Errorf("defaultErrorType(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestWriteAuthenticationError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteAuthenticationError(w, http.StatusUnauthorized, "Invalid API key provided.", "invalid_api_key")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var env OpenAIErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Error.Type != ErrorTypeAuthentication {
		t.Fatalf("type = %q, want %q", env.Error.Type, ErrorTypeAuthentication)
	}
	if env.Error.Code == nil || *env.Error.Code != "invalid_api_key" {
		t.Fatalf("code = %v, want invalid_api_key", env.Error.Code)
	}
}

func TestWritePermissionError(t *testing.T) {
	w := httptest.NewRecorder()
	WritePermissionError(w, http.StatusForbidden, "The model 'gpt-4o-mini' is not allowed for this API key.", "model", "model_not_allowed")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}

	var env OpenAIErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Error.Type != ErrorTypePermission {
		t.Fatalf("type = %q, want %q", env.Error.Type, ErrorTypePermission)
	}
	if env.Error.Param == nil || *env.Error.Param != "model" {
		t.Fatalf("param = %v, want model", env.Error.Param)
	}
	if env.Error.Code == nil || *env.Error.Code != "model_not_allowed" {
		t.Fatalf("code = %v, want model_not_allowed", env.Error.Code)
	}
}

func TestWriteRateLimitError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteRateLimitError(w, "Rate limit exceeded.", "rate_limit_exceeded")

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	var env OpenAIErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Error.Type != ErrorTypeRateLimit {
		t.Fatalf("type = %q, want %q", env.Error.Type, ErrorTypeRateLimit)
	}
	if env.Error.Code == nil || *env.Error.Code != "rate_limit_exceeded" {
		t.Fatalf("code = %v, want rate_limit_exceeded", env.Error.Code)
	}
}

func TestWriteInternalServerError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteInternalServerError(w, "Internal server error.", "internal_server_error")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var env OpenAIErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Error.Type != ErrorTypeServer {
		t.Fatalf("type = %q, want %q", env.Error.Type, ErrorTypeServer)
	}
	if env.Error.Code == nil || *env.Error.Code != "internal_server_error" {
		t.Fatalf("code = %v, want internal_server_error", env.Error.Code)
	}
}

func TestWriteGatewayTimeout(t *testing.T) {
	w := httptest.NewRecorder()
	WriteGatewayTimeout(w, "All configured upstream providers timed out.", "upstream_request_timeout")

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}

	var env OpenAIErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Error.Type != ErrorTypeServer {
		t.Fatalf("type = %q, want %q", env.Error.Type, ErrorTypeServer)
	}
	if env.Error.Code == nil || *env.Error.Code != "upstream_request_timeout" {
		t.Fatalf("code = %v, want upstream_request_timeout", env.Error.Code)
	}
}

func TestWriteOpenAIError(t *testing.T) {
	t.Run("sets correct status code", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteOpenAIError(w, http.StatusUnauthorized, "Unauthorized", "authentication_error", nil, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("got status %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("sets JSON content type", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteOpenAIError(w, http.StatusBadRequest, "bad", "invalid_request_error", nil, nil)
		ct := w.Header().Get("Content-Type")
		if ct != "application/json; charset=utf-8" {
			t.Errorf("unexpected content type: %q", ct)
		}
	})

	t.Run("body contains error message", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteOpenAIError(w, http.StatusBadRequest, "field required", "invalid_request_error", Ptr("model"), Ptr("missing_required_field"))

		var env OpenAIErrorEnvelope
		if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if env.Error.Message != "field required" {
			t.Errorf("message = %q, want %q", env.Error.Message, "field required")
		}
		if env.Error.Type != "invalid_request_error" {
			t.Errorf("type = %q, want %q", env.Error.Type, "invalid_request_error")
		}
		if env.Error.Param == nil || *env.Error.Param != "model" {
			t.Errorf("param = %v, want %q", env.Error.Param, "model")
		}
		if env.Error.Code == nil || *env.Error.Code != "missing_required_field" {
			t.Errorf("code = %v, want %q", env.Error.Code, "missing_required_field")
		}
	})

	t.Run("empty errType defaults to status-derived type", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteOpenAIError(w, http.StatusTooManyRequests, "slow down", "", nil, nil)

		var env OpenAIErrorEnvelope
		if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if env.Error.Type != "rate_limit_error" {
			t.Errorf("type = %q, want %q", env.Error.Type, "rate_limit_error")
		}
	})

	t.Run("nil param and code serialize as null", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteOpenAIError(w, http.StatusInternalServerError, "oops", "server_error", nil, nil)

		var env OpenAIErrorEnvelope
		if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if env.Error.Param != nil {
			t.Errorf("expected nil param, got %v", env.Error.Param)
		}
		if env.Error.Code != nil {
			t.Errorf("expected nil code, got %v", env.Error.Code)
		}
	})
}
