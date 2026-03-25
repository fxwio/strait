package middleware

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/response"
)

var (
	trustedProxyOnce     sync.Once
	trustedProxyNets     []*net.IPNet
	trustedProxyErr      error
	localLimiter         = newKeyedLocalLimiter()
)

func loadTrustedProxyCIDRs() ([]*net.IPNet, error) {
	trustedProxyOnce.Do(func() {
		trustedProxyNets, trustedProxyErr = parseCIDRs(config.GlobalConfig.Server.TrustedProxyCIDRs, "trusted proxy")
	})
	return trustedProxyNets, trustedProxyErr
}

func extractClientIP(r *http.Request) string {
	trustedCIDRs, err := loadTrustedProxyCIDRs()
	if err != nil {
		return remoteIP(r)
	}
	return extractClientIPFromTrustedProxy(r, trustedCIDRs)
}

func buildRateLimitIdentity(r *http.Request) (scope string, key string) {
	if authCtx, ok := GetClientAuthContext(r); ok && strings.TrimSpace(authCtx.Token) != "" {
		fp := authCtx.Fingerprint
		if fp != "" {
			return "token", "rate_limit:token:" + fp
		}
	}
	clientIP := extractClientIP(r)
	return "ip", "rate_limit:ip:" + clientIP
}

func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qps := int(config.GlobalConfig.Auth.RateLimitQPS)
		if qps <= 0 {
			qps = 1
		}
		burst := config.GlobalConfig.Auth.RateLimitBurst
		if burst <= 0 {
			burst = qps
		}

		if authCtx, ok := GetClientAuthContext(r); ok {
			if authCtx.RateLimitQPS > 0 {
				qps = int(authCtx.RateLimitQPS)
			}
			if authCtx.RateLimitBurst > 0 {
				burst = authCtx.RateLimitBurst
			}
		}

		scope, limitKey := buildRateLimitIdentity(r)
		allowed := localLimiter.Allow(limitKey, float64(qps), burst)

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(qps))
		w.Header().Set("X-RateLimit-Burst", strconv.Itoa(burst))
		w.Header().Set("X-RateLimit-Scope", scope)
		if authCtx, ok := GetClientAuthContext(r); ok {
			if authCtx.TokenName != "" {
				w.Header().Set("X-Gateway-Token", authCtx.TokenName)
			}
		}
		if scope == "ip" {
			w.Header().Set("X-RateLimit-Client-IP", extractClientIP(r))
		}
		if !allowed {
			w.Header().Set("Retry-After", "1")
			response.WriteRateLimitError(w, "Rate limit exceeded.", "rate_limit_exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
