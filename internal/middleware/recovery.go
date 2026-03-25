package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/fxwio/strait/internal/response"
	"github.com/fxwio/strait/pkg/logger"
)

// RecoveryMiddleware 兜底捕获 handler panic，避免单个请求把整个网关进程打崩。
// 这里返回 OpenAI 风格错误，保证客户端拿到稳定的协议响应。
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			requestID := ""
			traceID := ""
			if meta, ok := GetRequestMeta(r); ok {
				requestID = meta.RequestID
				traceID = meta.TraceID
				if requestID != "" && w.Header().Get("X-Request-ID") == "" {
					w.Header().Set("X-Request-ID", requestID)
				}
			}

			logger.Log.Error("CRITICAL: Panic recovered in HTTP handler",
				"request_id", requestID,
				"trace_id", traceID,
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()),
			)


			response.WriteInternalServerError(w, "Internal server error.", "internal_server_error")
		}()

		next.ServeHTTP(w, r)
	})
}
