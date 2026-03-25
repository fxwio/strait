package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/fxwio/strait/internal/config"
	gatewaymetrics "github.com/fxwio/strait/internal/metrics"
)

// MetricsMiddleware 负责上报 Prometheus 指标。
// 计数和时延都在请求结束后统一记录，in-flight 则在请求生命周期内增减。
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		_, r = ensureRequestState(r)
		route := metricsRouteLabel(r)

		gatewaymetrics.RequestsInFlight.WithLabelValues(route).Inc()
		defer gatewaymetrics.RequestsInFlight.WithLabelValues(route).Dec()

		wrappedWriter := newResponseObserver(w)

		next.ServeHTTP(wrappedWriter, r)

		if wrappedWriter.statusCode == 0 {
			wrappedWriter.statusCode = http.StatusOK
		}

		durationSeconds := time.Since(start).Seconds()
		provider := "unknown"
		targetModel := "unknown"

		if gCtx, ok := GetGatewayContext(r); ok {
			if gCtx.TargetProvider != "" {
				provider = gCtx.TargetProvider
			}
			if gCtx.TargetModel != "" {
				targetModel = gCtx.TargetModel
			}
		}

		statusCode := strconv.Itoa(wrappedWriter.statusCode)

		gatewaymetrics.RequestTotal.WithLabelValues(
			provider,
			targetModel,
			statusCode,
			"none",
		).Inc()

		gatewaymetrics.RequestDuration.WithLabelValues(
			provider,
			targetModel,
			statusCode,
			"none",
		).Observe(durationSeconds)
	})
}

func metricsRouteLabel(r *http.Request) string {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		return "chat_completions"
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		return "health"
	case r.Method == http.MethodGet && r.URL.Path == "/health/live":
		return "health_live"
	case r.Method == http.MethodGet && r.URL.Path == "/health/ready":
		return "health_ready"
	case r.Method == http.MethodGet && r.URL.Path == config.MetricsPath:
		return "metrics"
	default:
		return "unknown"
	}
}
