package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyContextMiddleware_InjectsIncludeUsageForStreamRequest(t *testing.T) {
	payload := `{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":"hi"}],
		"stream":true
	}`

	handler := BodyContextMiddleware(DefaultMaxRequestBodyBytes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			t.Fatal("expected request body context")
		}

		if !bodyCtx.IsStream {
			t.Fatal("expected IsStream=true")
		}

		if bodyCtx.RequestedModel != "gpt-4o" {
			t.Fatalf("RequestedModel = %q, want %q", bodyCtx.RequestedModel, "gpt-4o")
		}

		if !bodyCtx.StreamOptionsInjected {
			t.Fatal("expected StreamOptionsInjected=true")
		}

		if bytes.Equal(bodyCtx.RawBody, bodyCtx.UpstreamBody) {
			t.Fatal("expected upstream body to be different from raw body")
		}

		var rawJSON map[string]interface{}
		if err := json.Unmarshal(bodyCtx.RawBody, &rawJSON); err != nil {
			t.Fatalf("unmarshal raw body: %v", err)
		}
		if _, exists := rawJSON["stream_options"]; exists {
			t.Fatal("raw body should remain untouched")
		}

		reqBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !bytes.Equal(reqBody, bodyCtx.UpstreamBody) {
			t.Fatal("request body should equal upstream body")
		}

		var upstreamJSON map[string]interface{}
		if err := json.Unmarshal(bodyCtx.UpstreamBody, &upstreamJSON); err != nil {
			t.Fatalf("unmarshal upstream body: %v", err)
		}

		streamOptions, ok := upstreamJSON["stream_options"].(map[string]interface{})
		if !ok {
			t.Fatal("expected stream_options object")
		}

		includeUsage, ok := streamOptions["include_usage"].(bool)
		if !ok || !includeUsage {
			t.Fatal("expected stream_options.include_usage=true")
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
	}
}

func TestBodyContextMiddleware_PreservesExistingIncludeUsage(t *testing.T) {
	payload := `{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":false}}`

	handler := BodyContextMiddleware(DefaultMaxRequestBodyBytes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			t.Fatal("expected request body context")
		}

		if !bodyCtx.IsStream {
			t.Fatal("expected IsStream=true")
		}

		if bodyCtx.StreamOptionsInjected {
			t.Fatal("expected StreamOptionsInjected=false")
		}

		if !bytes.Equal(bodyCtx.RawBody, bodyCtx.UpstreamBody) {
			t.Fatal("expected upstream body to remain equal to raw body")
		}

		reqBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}

		if !bytes.Equal(reqBody, []byte(payload)) {
			t.Fatalf("request body changed unexpectedly: got=%s", string(reqBody))
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
	}
}

func TestBodyContextMiddleware_InjectsIncludeUsageIntoExistingStreamOptionsObject(t *testing.T) {
	payload := `{"model":"gpt-4o","stream":true,"stream_options":{"foo":"bar"}}`

	handler := BodyContextMiddleware(DefaultMaxRequestBodyBytes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			t.Fatal("expected request body context")
		}

		if !bodyCtx.IsStream {
			t.Fatal("expected IsStream=true")
		}
		if !bodyCtx.StreamOptionsInjected {
			t.Fatal("expected StreamOptionsInjected=true")
		}

		var upstreamJSON map[string]interface{}
		if err := json.Unmarshal(bodyCtx.UpstreamBody, &upstreamJSON); err != nil {
			t.Fatalf("unmarshal upstream body: %v", err)
		}
		streamOptions, ok := upstreamJSON["stream_options"].(map[string]interface{})
		if !ok {
			t.Fatal("expected stream_options object")
		}
		if streamOptions["foo"] != "bar" {
			t.Fatalf("stream_options.foo = %v, want bar", streamOptions["foo"])
		}
		includeUsage, ok := streamOptions["include_usage"].(bool)
		if !ok || !includeUsage {
			t.Fatal("expected stream_options.include_usage=true")
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
	}
}

func TestBodyContextMiddleware_FastPathInjectsIncludeUsageWithoutTouchingRawBody(t *testing.T) {
	payload := "{\n  \"model\":\"gpt-4o\",\n  \"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\n  \"stream\":true\n}\n"

	handler := BodyContextMiddleware(DefaultMaxRequestBodyBytes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			t.Fatal("expected request body context")
		}

		if !bodyCtx.StreamOptionsInjected {
			t.Fatal("expected fast-path stream_options injection")
		}
		if string(bodyCtx.RawBody) != payload {
			t.Fatalf("raw body changed: %q", bodyCtx.RawBody)
		}
		if !strings.HasSuffix(string(bodyCtx.UpstreamBody), "}\n") {
			t.Fatalf("expected trailing newline to be preserved, got %q", bodyCtx.UpstreamBody)
		}

		var upstreamJSON map[string]interface{}
		if err := json.Unmarshal(bodyCtx.UpstreamBody, &upstreamJSON); err != nil {
			t.Fatalf("unmarshal upstream body: %v", err)
		}
		streamOptions, ok := upstreamJSON["stream_options"].(map[string]interface{})
		if !ok {
			t.Fatal("expected stream_options object")
		}
		if includeUsage, _ := streamOptions["include_usage"].(bool); !includeUsage {
			t.Fatal("expected include_usage=true")
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
	}
}

func TestBodyContextMiddleware_CapturesDecodeErrorWithoutRejectingEarly(t *testing.T) {
	payload := `{"model":"gpt-4o","messages":"invalid"}`

	handler := BodyContextMiddleware(DefaultMaxRequestBodyBytes, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyCtx, ok := GetRequestBodyContext(r)
		if !ok {
			t.Fatal("expected request body context")
		}

		if bodyCtx.DecodeError == nil {
			t.Fatal("expected DecodeError to be populated")
		}

		if bodyCtx.RequestedModel != "" {
			t.Fatalf("RequestedModel = %q, want empty", bodyCtx.RequestedModel)
		}

		if bodyCtx.IsStream {
			t.Fatal("expected IsStream=false when decode fails")
		}

		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusAccepted)
	}
}
