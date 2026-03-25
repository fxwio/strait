package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const RequestMetaContextKey contextKey = "request_meta_ctx"

type RequestMetaContext struct {
	RequestID   string
	TraceID     string
	TraceParent string
	TraceState  string
}

var requestIDFallbackCounter uint64

// RequestMetaMiddleware 为每个请求注入 request_id / traceparent / trace_id。
// - 优先复用上游传入的 X-Request-ID
// - 优先复用合法的 traceparent
// - 否则由网关生成新的 request_id 和 traceparent
func RequestMetaMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := buildRequestMeta(r)

		// 先把头打到响应上，确保后续无论成功还是失败都能带回去
		w.Header().Set("X-Request-ID", meta.RequestID)
		w.Header().Set("Traceparent", meta.TraceParent)
		if meta.TraceState != "" {
			w.Header().Set("Tracestate", meta.TraceState)
		}

		r = putRequestMeta(r, meta)
		next.ServeHTTP(w, r)
	})
}

func GetRequestMeta(r *http.Request) (*RequestMetaContext, bool) {
	if r == nil {
		return nil, false
	}
	if state, ok := getRequestState(r.Context()); ok && state.HasMeta {
		return &state.Meta, true
	}

	ctxVal := r.Context().Value(RequestMetaContextKey)
	if ctxVal == nil {
		return nil, false
	}

	meta, ok := ctxVal.(*RequestMetaContext)
	if !ok || meta == nil {
		return nil, false
	}

	return meta, true
}

func buildRequestMeta(r *http.Request) RequestMetaContext {
	requestID := sanitizeRequestID(r.Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = "req_" + generateHex(12)
	}

	traceParent := strings.ToLower(strings.TrimSpace(r.Header.Get("Traceparent")))
	if !isValidTraceParent(traceParent) {
		traceParent = generateTraceParent()
	}

	traceState := strings.TrimSpace(r.Header.Get("Tracestate"))

	return RequestMetaContext{
		RequestID:   requestID,
		TraceID:     traceIDFromTraceParent(traceParent),
		TraceParent: traceParent,
		TraceState:  traceState,
	}
}

func sanitizeRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return ""
	}

	for _, ch := range raw {
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == ':' || ch == '/' {
			continue
		}
		return ""
	}

	return raw
}

func isValidTraceParent(tp string) bool {
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		return false
	}

	version := parts[0]
	traceID := parts[1]
	parentID := parts[2]
	traceFlags := parts[3]

	if !isLowerHex(version, 2) || !isLowerHex(traceID, 32) || !isLowerHex(parentID, 16) || !isLowerHex(traceFlags, 2) {
		return false
	}
	if isAllZeros(traceID) || isAllZeros(parentID) {
		return false
	}

	return true
}

func traceIDFromTraceParent(tp string) string {
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		return ""
	}
	return parts[1]
}

func isLowerHex(s string, expectedLen int) bool {
	if len(s) != expectedLen {
		return false
	}
	for _, ch := range s {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func isAllZeros(s string) bool {
	for _, ch := range s {
		if ch != '0' {
			return false
		}
	}
	return true
}

func generateTraceParent() string {
	return "00-" + generateHex(16) + "-" + generateHex(8) + "-01"
}

func generateHex(nBytes int) string {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}

	// 极端情况下的兜底，避免生成空值
	seed := uint64(time.Now().UnixNano()) + atomic.AddUint64(&requestIDFallbackCounter, 1)
	for i := range buf {
		shift := uint((i % 8) * 8)
		buf[i] = byte((seed >> shift) & 0xff)
	}
	return hex.EncodeToString(buf)
}
