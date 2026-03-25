package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/pkg/logger"
)

type testLogHandler struct {
	mu      sync.Mutex
	entries []map[string]any
}

func (h *testLogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *testLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := make(map[string]any)
	entry["msg"] = r.Message
	entry["level"] = r.Level.String()
	r.Attrs(func(a slog.Attr) bool {
		entry[a.Key] = a.Value.Any()
		return true
	})
	h.entries = append(h.entries, entry)
	return nil
}
func (h *testLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *testLogHandler) WithGroup(name string) slog.Handler       { return h }

func TestAccessLogMiddleware_EmitsUpstreamTrace(t *testing.T) {
	config.GlobalConfig = &config.Config{}

	handler := &testLogHandler{}
	prevLogger := logger.Log
	logger.Log = slog.New(handler)
	defer func() { logger.Log = prevLogger }()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(context.WithValue(req.Context(), GatewayContextKey, &model.GatewayContext{
		RequestedModel:       "smart",
		TargetProvider:       "anthropic",
		TargetModel:          "claude-3-5-sonnet",
		RouteSelectionPolicy: "configured_order",
		RouteCandidates: model.RouteCandidateTraceList{
			{Provider: "openai", Priority: 1},
			{Provider: "anthropic", Priority: 2},
		},
		AttemptedProviders: []string{"openai", "anthropic"},
		UpstreamAttempts: model.UpstreamAttemptTraceList{
			{
				Provider:      "openai",
				ProviderIndex: 0,
				Attempt:       1,
				AttemptBudget: 2,
				StatusCode:    503,
				Result:        "retry_exhausted",
				Reason:        "status_503",
				DurationMs:    120,
			},
			{
				Provider:      "anthropic",
				ProviderIndex: 1,
				Attempt:       1,
				AttemptBudget: 2,
				StatusCode:    200,
				Result:        "returned_response",
				Reason:        "200",
				DurationMs:    80,
			},
		},
		FailoverEvents: model.UpstreamFailoverTraceList{
			{
				FromProvider:  "openai",
				ToProvider:    "anthropic",
				ProviderIndex: 0,
				FailoverCount: 1,
				Reason:        "status_503",
			},
		},
		StreamOutcome:      "completed",
		StreamChunks:       3,
		StreamBytes:        64,
		FinalStatusCode:    503,
		FinalErrorType:     "server_error",
		FinalErrorCode:     "all_upstreams_unavailable",
		FinalFailureReason: "status_503",
	}))
	req = req.WithContext(context.WithValue(req.Context(), RequestMetaContextKey, &RequestMetaContext{
		RequestID:   "req-test",
		TraceID:     "trace-test",
		TraceParent: "00-trace-test-span-test-01",
	}))

	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Gateway-Upstream-Provider", "anthropic")
		w.Header().Set("X-Gateway-Upstream-Retries", "1")
		w.Header().Set("X-Gateway-Failovers", "1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mw := AccessLogMiddleware(next)
	mw.ServeHTTP(rec, req)

	if len(handler.entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(handler.entries))
	}

	entry := handler.entries[0]
	for _, key := range []string{
		"requested_model",
		"route_selection_policy",
		"stream_outcome",
		"final_status_code",
		"final_error_code",
		"final_failure_reason",
	} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("expected log field %q in access log, got entry=%v", key, entry)
		}
	}

	for _, key := range []string{
		"route_candidate_count",
		"route_candidates",
		"attempted_providers",
		"upstream_attempt_count",
		"upstream_attempts",
		"failover_events",
		"stream_chunks",
		"stream_bytes",
		"final_error_type",
	} {
		if _, ok := entry[key]; ok {
			t.Fatalf("did not expect verbose access-log field %q, got entry=%v", key, entry)
		}
	}
}

func TestAccessLogMiddleware_UsesGatewayTerminalStatusWithoutWrite(t *testing.T) {
	config.GlobalConfig = &config.Config{}

	handler := &testLogHandler{}
	prevLogger := logger.Log
	logger.Log = slog.New(handler)
	defer func() { logger.Log = prevLogger }()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(context.WithValue(req.Context(), GatewayContextKey, &model.GatewayContext{
		TargetProvider:     "openai",
		TargetModel:        "gpt-4o",
		FinalStatusCode:    499,
		FinalErrorType:     "server_error",
		FinalErrorCode:     "client_canceled",
		FinalFailureReason: "client_canceled",
	}))

	rec := httptest.NewRecorder()
	mw := AccessLogMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	mw.ServeHTTP(rec, req)

	if len(handler.entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(handler.entries))
	}

	entry := handler.entries[0]
	if entry["status_code"] != int64(499) {
		t.Errorf("expected access log status_code=499, got %v", entry["status_code"])
	}
	if entry["final_error_code"] != "client_canceled" {
		t.Errorf("expected access log final_error_code=client_canceled, got %v", entry["final_error_code"])
	}
}

func TestAccessLogMiddleware_CountsReaderFromBytesAndPreservesFlusher(t *testing.T) {
	config.GlobalConfig = &config.Config{}

	handler := &testLogHandler{}
	prevLogger := logger.Log
	logger.Log = slog.New(handler)
	defer func() { logger.Log = prevLogger }()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()

	mw := AccessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Fatal("expected wrapped writer to preserve http.Flusher")
		}
		rf, ok := w.(io.ReaderFrom)
		if !ok {
			t.Fatal("expected wrapped writer to preserve io.ReaderFrom")
		}
		if _, err := rf.ReadFrom(strings.NewReader("hello")); err != nil {
			t.Fatalf("ReadFrom error: %v", err)
		}
	}))
	mw.ServeHTTP(rec, req)

	if len(handler.entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(handler.entries))
	}

	if got := handler.entries[0]["response_bytes"]; got != int64(5) {
		t.Fatalf("response_bytes = %v, want 5", got)
	}
}
