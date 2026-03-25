package router

import (
	"encoding/json"
	"net/http"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/middleware"
	"github.com/fxwio/strait/internal/proxy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type healthResponse struct {
	Status           string   `json:"status"`
	DegradedFeatures []string `json:"degraded_features,omitempty"`
}

func NewRouter() http.Handler {
	mux := http.NewServeMux()

	finalChatHandler := buildChatHandler()

	mux.Handle("POST /v1/chat/completions", finalChatHandler)

	metricsBaseHandler := promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{},
	)
	metricsInstrumentedHandler := promhttp.InstrumentMetricHandler(prometheus.DefaultRegisterer, metricsBaseHandler)
	protectedMetricsHandler := middleware.MetricsEndpointMiddleware(metricsInstrumentedHandler)
	mux.Handle(config.MetricsPath, protectedMetricsHandler)

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{Status: "alive"})
	})

	readyHandler := func(w http.ResponseWriter, r *http.Request) {
		upstreamStatuses := proxy.GetEffectiveUpstreamStatuses()
		resp, httpStatus := buildReadyHealthResponse(upstreamStatuses)
		writeJSON(w, httpStatus, resp)
	}
	mux.HandleFunc("GET /health/ready", readyHandler)
	mux.HandleFunc("GET /health", readyHandler)

	return middleware.RecoveryMiddleware(drainingMiddleware(mux))
}

func buildReadyHealthResponse(
	upstreamStatuses map[string]proxy.ProviderDependencyStatus,
) (healthResponse, int) {
	resp := healthResponse{
		Status: "ok",
	}

	var degraded []string
	if providersNeedHealthVerification(upstreamStatuses) {
		degraded = append(degraded, "provider_health_unverified")
	}
	if len(degraded) > 0 {
		resp.Status = "degraded"
		resp.DegradedFeatures = degraded
	}

	httpStatus := http.StatusOK
	if len(upstreamStatuses) > 0 {
		allUnhealthy := true
		for _, s := range upstreamStatuses {
			if s.Healthy {
				allUnhealthy = false
				break
			}
		}
		if allUnhealthy {
			resp.Status = "unhealthy"
			resp.DegradedFeatures = append(resp.DegradedFeatures, "all_providers_down")
			httpStatus = http.StatusServiceUnavailable
		}
	}

	return resp, httpStatus
}

func providersNeedHealthVerification(upstreamStatuses map[string]proxy.ProviderDependencyStatus) bool {
	if len(upstreamStatuses) == 0 {
		return false
	}

	hasUnverifiedProvider := false
	for _, status := range upstreamStatuses {
		if status.Source != "bootstrap" && status.Healthy {
			return false
		}
		if status.Source == "bootstrap" && status.BreakerState == "" {
			hasUnverifiedProvider = true
		}
	}

	return hasUnverifiedProvider
}

func buildChatHandler() http.Handler {
	var handler http.Handler = proxy.NewGatewayProxy()

	handler = middleware.MetricsMiddleware(handler)
	handler = middleware.ModelRouterMiddleware(handler)
	handler = middleware.ModelAllowlistMiddleware(handler)
	handler = middleware.BodyContextMiddleware(middleware.DefaultMaxRequestBodyBytes, handler)
	handler = middleware.RateLimitMiddleware(handler)
	handler = middleware.AuthMiddleware(handler)
	handler = middleware.AccessLogMiddleware(handler)
	handler = middleware.RequestMetaMiddleware(handler)

	return handler
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
