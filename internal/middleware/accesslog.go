package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/fxwio/strait/pkg/logger"
)

func AccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		_, r = ensureRequestState(r)
		recorder := newResponseObserver(w)

		next.ServeHTTP(recorder, r)

		gatewayCtx, _ := GetGatewayContext(r)

		if recorder.statusCode == 0 {
			if gatewayCtx != nil && gatewayCtx.FinalStatusCode > 0 {
				recorder.statusCode = gatewayCtx.FinalStatusCode
			} else {
				recorder.statusCode = http.StatusOK
			}
		}

		clientIP := extractClientIP(r)
		rateLimitScope := recorder.Header().Get("X-RateLimit-Scope")
		upstreamProvider := strings.TrimSpace(recorder.Header().Get("X-Gateway-Upstream-Provider"))
		upstreamRetries := strings.TrimSpace(recorder.Header().Get("X-Gateway-Upstream-Retries"))
		failovers := strings.TrimSpace(recorder.Header().Get("X-Gateway-Failovers"))

		fields := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status_code", recorder.statusCode,
			"response_bytes", recorder.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", clientIP,
			"user_agent", r.UserAgent(),
		}

		if rateLimitScope != "" {
			fields = append(fields, "rate_limit_scope", rateLimitScope)
		}
		if upstreamProvider != "" {
			fields = append(fields, "upstream_provider", upstreamProvider)
		}
		if upstreamRetries != "" {
			fields = append(fields, "upstream_retries", upstreamRetries)
		}
		if failovers != "" {
			fields = append(fields, "failover_count", failovers)
		}

		if gatewayCtx != nil {
			if gatewayCtx.TargetProvider != "" {
				fields = append(fields, "provider", gatewayCtx.TargetProvider)
			}
			if gatewayCtx.RequestedModel != "" {
				fields = append(fields, "requested_model", gatewayCtx.RequestedModel)
			}
			if gatewayCtx.TargetModel != "" {
				fields = append(fields, "model", gatewayCtx.TargetModel)
			}
			if gatewayCtx.RouteSelectionPolicy != "" {
				fields = append(fields, "route_selection_policy", gatewayCtx.RouteSelectionPolicy)
			}
			if gatewayCtx.StreamOutcome != "" {
				fields = append(fields, "stream_outcome", gatewayCtx.StreamOutcome)
			}
			if gatewayCtx.FinalStatusCode > 0 {
				fields = append(fields, "final_status_code", gatewayCtx.FinalStatusCode)
			}
			if gatewayCtx.FinalErrorCode != "" {
				fields = append(fields, "final_error_code", gatewayCtx.FinalErrorCode)
			}
			if gatewayCtx.FinalFailureReason != "" {
				fields = append(fields, "final_failure_reason", gatewayCtx.FinalFailureReason)
			}
		}

		if meta, ok := GetRequestMeta(r); ok {
			fields = append(fields,
				"request_id", meta.RequestID,
				"trace_id", meta.TraceID,
			)
		}

		logger.Log.Info("HTTP request completed", fields...)
	})
}
