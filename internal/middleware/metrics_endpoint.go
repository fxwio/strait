package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/strait/internal/config"
)

var (
	metricsCIDROnce sync.Once
	metricsCIDRs    []*net.IPNet
	metricsCIDRErr  error

	metricsLimiterOnce sync.Once
	metricsLimiter     *localTokenBucket
)

const (
	metricsEndpointRateLimitRPS   = 5
	metricsEndpointRateLimitBurst = 10
)

func resetMetricsEndpointRuntimeForTest() {
	metricsCIDROnce = sync.Once{}
	metricsCIDRs = nil
	metricsCIDRErr = nil
	metricsLimiterOnce = sync.Once{}
	metricsLimiter = nil
}

func getMetricsLimiter() *localTokenBucket {
	metricsLimiterOnce.Do(func() {
		metricsLimiter = newLocalTokenBucket(
			metricsEndpointRateLimitRPS,
			metricsEndpointRateLimitBurst,
			time.Now(),
		)
	})

	return metricsLimiter
}

func loadMetricsAllowedCIDRs() ([]*net.IPNet, error) {
	metricsCIDROnce.Do(func() {
		metricsCIDRs, metricsCIDRErr = parseCIDRs(
			config.GlobalConfig.Metrics.AllowedCIDRs,
			"metrics allowed",
		)
	})

	return metricsCIDRs, metricsCIDRErr
}

func isMetricsIPAllowed(r *http.Request) bool {
	allowedCIDRs, err := loadMetricsAllowedCIDRs()
	if err != nil {
		return false
	}

	// /metrics 保护继续只看直连来源 IP，不信任 XFF / X-Real-IP。
	return ipInCIDRs(remoteIP(r), allowedCIDRs)
}

func hasValidMetricsBearerToken(r *http.Request) bool {
	expected := strings.TrimSpace(config.GlobalConfig.Metrics.BearerToken)
	if expected == "" {
		return false
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		return false
	}

	return token == expected
}

func MetricsEndpointMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasValidMetricsBearerToken(r) && !isMetricsIPAllowed(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		limiter := getMetricsLimiter()
		if limiter != nil && !limiter.Allow(float64(metricsEndpointRateLimitRPS), metricsEndpointRateLimitBurst, time.Now()) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
