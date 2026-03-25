package proxy

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/fxwio/strait/internal/config"
	gatewaymetrics "github.com/fxwio/strait/internal/metrics"
	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/pkg/logger"
	"github.com/sony/gobreaker"
)

// CircuitBreakerTransport 包装标准的 RoundTripper，并按 provider+host 维度执行熔断。
type CircuitBreakerTransport struct {
	Transport http.RoundTripper
}

var (
	cbMap = make(map[string]*gobreaker.CircuitBreaker)
	cbMu  sync.RWMutex
)

func getBreaker(key string) *gobreaker.CircuitBreaker {
	cbMu.RLock()
	cb, exists := cbMap[key]
	cbMu.RUnlock()
	if exists {
		return cb
	}

	cbMu.Lock()
	defer cbMu.Unlock()

	if cb, exists = cbMap[key]; exists {
		return cb
	}

	interval, _ := time.ParseDuration(config.GlobalConfig.Upstream.BreakerInterval)
	if interval <= 0 {
		interval = 10 * time.Second
	}

	timeout, _ := time.ParseDuration(config.GlobalConfig.Upstream.BreakerTimeout)
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	minimumRequests := config.GlobalConfig.Upstream.BreakerMinimumRequests
	failureRatioThreshold := config.GlobalConfig.Upstream.BreakerFailureRatio
	maxHalfOpenRequests := config.GlobalConfig.Upstream.BreakerHalfOpenRequests

	settings := gobreaker.Settings{
		Name:        key,
		MaxRequests: maxHalfOpenRequests,
		Interval:    interval,
		Timeout:     timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < minimumRequests || counts.Requests == 0 {
				return false
			}
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return failureRatio >= failureRatioThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			setCircuitBreakerMetric(name, to)
			logger.Log.Warn(
				"Circuit breaker state changed",
				"breaker", name,
				"from", from.String(),
				"to", to.String(),
			)
		},
	}

	cb = gobreaker.NewCircuitBreaker(settings)
	cbMap[key] = cb
	setCircuitBreakerMetric(key, gobreaker.StateClosed)
	return cb
}

func (c *CircuitBreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	breakerKey := breakerKeyForRequest(req)

	cb := getBreaker(breakerKey)
	respInterface, err := cb.Execute(func() (interface{}, error) {
		resp, err := c.Transport.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return resp, fmt.Errorf("upstream error: status %d", resp.StatusCode)
		}
		return resp, nil
	})
	if err != nil {
		logger.Log.Error(
			"Proxy request failed or circuit breaker open",
			"breaker", breakerKey,
			"error", err,
		)
		if resp, ok := respInterface.(*http.Response); ok && resp != nil {
			return resp, nil
		}
		return nil, err
	}

	return respInterface.(*http.Response), nil
}

func breakerKeyForRequest(req *http.Request) string {
	breakerKey := req.URL.Host
	if gatewayCtx, err := getGatewayContext(req); err == nil && gatewayCtx.TargetProvider != "" {
		breakerKey = gatewayCtx.TargetProvider + "@" + req.URL.Host
	}
	return breakerKey
}

func breakerKeyForProvider(provider model.ProviderRoute) string {
	if provider.Name == "" {
		return ""
	}
	if parsed, err := parseAndCacheBaseURL(provider.BaseURL); err == nil && parsed.Host != "" {
		return provider.Name + "@" + parsed.Host
	}
	return ""
}

func resolveCircuitBreakerHealth(provider model.ProviderRoute) (known bool, healthy bool) {
	known, state := circuitBreakerStateForProvider(provider)
	if !known {
		return false, true
	}
	return true, state != gobreaker.StateOpen
}

func circuitBreakerStateForProvider(provider model.ProviderRoute) (known bool, state gobreaker.State) {
	breakerKey := breakerKeyForProvider(provider)
	if breakerKey == "" {
		return false, gobreaker.StateClosed
	}

	cbMu.RLock()
	cb, exists := cbMap[breakerKey]
	cbMu.RUnlock()
	if !exists || cb == nil {
		return false, gobreaker.StateClosed
	}

	return true, cb.State()
}

func setCircuitBreakerMetric(name string, state gobreaker.State) {
	var value float64
	switch state {
	case gobreaker.StateClosed:
		value = 0
	case gobreaker.StateHalfOpen:
		value = 1
	case gobreaker.StateOpen:
		value = 2
	default:
		value = -1
	}
	gatewaymetrics.CircuitBreakerState.WithLabelValues(name).Set(value)
}
