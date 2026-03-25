package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestMetaMiddleware_PreservesIncomingRequestID(t *testing.T) {
	handler := RequestMetaMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta, ok := GetRequestMeta(r)
		if !ok {
			t.Fatal("request meta missing")
		}
		if meta.RequestID != "demo-request-123" {
			t.Fatalf("expected request id demo-request-123, got %s", meta.RequestID)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", "demo-request-123")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Request-ID"); got != "demo-request-123" {
		t.Fatalf("expected response X-Request-ID demo-request-123, got %s", got)
	}
}

func TestRequestMetaMiddleware_GeneratesTraceparent(t *testing.T) {
	handler := RequestMetaMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta, ok := GetRequestMeta(r)
		if !ok {
			t.Fatal("request meta missing")
		}
		if meta.TraceID == "" {
			t.Fatal("trace id should not be empty")
		}
		if !isValidTraceParent(meta.TraceParent) {
			t.Fatalf("generated invalid traceparent: %s", meta.TraceParent)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	tp := rr.Header().Get("Traceparent")
	if tp == "" {
		t.Fatal("expected Traceparent header to be set")
	}
	if !isValidTraceParent(strings.ToLower(tp)) {
		t.Fatalf("response traceparent invalid: %s", tp)
	}
}

func TestRequestMetaMiddleware_UsesIncomingTraceparent(t *testing.T) {
	incoming := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

	handler := RequestMetaMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta, ok := GetRequestMeta(r)
		if !ok {
			t.Fatal("request meta missing")
		}
		if meta.TraceParent != incoming {
			t.Fatalf("expected traceparent %s, got %s", incoming, meta.TraceParent)
		}
		if meta.TraceID != "0af7651916cd43dd8448eb211c80319c" {
			t.Fatalf("unexpected trace id: %s", meta.TraceID)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Traceparent", incoming)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Traceparent"); got != incoming {
		t.Fatalf("expected response traceparent %s, got %s", incoming, got)
	}
}
