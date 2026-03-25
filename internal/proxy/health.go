package proxy

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fxwio/strait/internal/adapter"
	"github.com/fxwio/strait/internal/config"
	gatewaymetrics "github.com/fxwio/strait/internal/metrics"
	"github.com/fxwio/strait/internal/middleware"
	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/pkg/logger"
	"github.com/sony/gobreaker"
)

type ProviderDependencyStatus struct {
	Name         string    `json:"name"`
	BaseURL      string    `json:"base_url"`
	Healthy      bool      `json:"healthy"`
	BreakerState string    `json:"breaker_state,omitempty"`
	StatusCode   int       `json:"status_code,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	Source       string    `json:"source,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

var (
	providerHealthMu      sync.RWMutex
	providerHealthMap     = make(map[string]ProviderDependencyStatus)
	providerMonitorOnce   sync.Once
	providerProbeMu       sync.Mutex
	providerProbeClient   *http.Client
	providerProbeClientMu sync.RWMutex
)

type providerHealthVerdict uint8

const (
	healthVerdictInconclusive providerHealthVerdict = iota
	healthVerdictHealthy
	healthVerdictUnhealthy
)

func initUpstreamHealthMonitor(transport http.RoundTripper) {
	providerMonitorOnce.Do(func() {
		middleware.SetProviderHealthResolver(resolveProviderHealth)
		RefreshProviderHealthSnapshot()

		interval, _ := time.ParseDuration(config.GlobalConfig.Upstream.HealthCheckInterval)
		if interval <= 0 {
			interval = 15 * time.Second
		}

		client := &http.Client{Transport: transport}
		setProviderProbeClient(client)

		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				probeConfiguredProviders(client)
				<-ticker.C
			}
		}()
	})
}

// RefreshProviderHealthSnapshot synchronises the in-memory provider dependency
// map with the current config snapshot. Existing provider statuses are
// preserved, new providers are bootstrapped as healthy, and removed providers
// are dropped from readiness output and metrics immediately.
func RefreshProviderHealthSnapshot() {
	providerHealthMu.Lock()
	defer providerHealthMu.Unlock()

	now := time.Now()
	next := make(map[string]ProviderDependencyStatus, len(config.GlobalConfig.Providers))

	for _, provider := range config.GlobalConfig.Providers {
		if status, ok := providerHealthMap[provider.Name]; ok {
			status.BaseURL = provider.BaseURL
			next[provider.Name] = status
			setProviderHealthMetric(provider.Name, status.Healthy)
			continue
		}

		status := ProviderDependencyStatus{
			Name:      provider.Name,
			BaseURL:   provider.BaseURL,
			Healthy:   true,
			Source:    "bootstrap",
			UpdatedAt: now,
		}
		next[provider.Name] = status
		setProviderHealthMetric(provider.Name, true)
	}

	for providerName := range providerHealthMap {
		if _, ok := next[providerName]; ok {
			continue
		}
		gatewaymetrics.UpstreamProviderHealth.DeleteLabelValues(providerName)
	}

	providerHealthMap = next
}

func resolveProviderHealth(candidate model.ProviderRoute) (known bool, healthy bool) {
	breakerKnown, breakerHealthy := resolveCircuitBreakerHealth(candidate)
	if breakerKnown && !breakerHealthy {
		return true, false
	}

	providerHealthMu.RLock()
	defer providerHealthMu.RUnlock()

	status, ok := providerHealthMap[candidate.Name]
	if !ok {
		return false, true
	}
	return true, status.Healthy
}

func probeProvider(client *http.Client, provider config.ProviderConfig) {
	timeout, _ := time.ParseDuration(config.GlobalConfig.Upstream.HealthCheckTimeout)
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var req *http.Request
	var err error

	// Try minimal-token probe if a model and API key are available.
	if len(provider.Models) > 0 {
		adapterProvider := adapter.GetProvider(provider.Name)
		targetURL, urlErr := parseAndCacheBaseURL(provider.BaseURL)
		if urlErr == nil {
			req, err = adapterProvider.GenerateProbeRequest(targetURL, provider.APIKey, provider.Models[0])
		}
	}

	// Fallback to simple GET probe.
	if req == nil {
		probeURL := strings.TrimRight(provider.BaseURL, "/")
		if path := strings.TrimSpace(provider.HealthCheckPath); path != "" {
			probeURL += "/" + strings.TrimLeft(path, "/")
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	} else {
		req = req.WithContext(ctx)
	}

	if err != nil {
		updateProviderHealth(provider.Name, provider.BaseURL, false, 0, err, "active")
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		updateProviderHealth(provider.Name, provider.BaseURL, false, 0, err, "active")
		return
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Log.Debug(
				"Failed to close upstream health probe body",
				"provider", provider.Name,
				"error", closeErr,
			)
		}
	}()

	verdict := classifyActiveProbeVerdict(provider, resp, nil)
	if verdict == healthVerdictInconclusive {
		logger.Log.Debug(
			"Skipping inconclusive upstream health probe result",
			"provider", provider.Name,
			"status_code", resp.StatusCode,
		)
		return
	}

	healthy := verdict == healthVerdictHealthy
	var healthErr error
	if !healthy {
		healthErr = errStatusUnhealthy(resp.StatusCode)
	}
	updateProviderHealth(provider.Name, provider.BaseURL, healthy, resp.StatusCode, healthErr, "active")
}

func markPassiveProbeResult(providerName, baseURL string, resp *http.Response, err error) {
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}

	healthy := classifyPassiveProbeVerdict(resp, err) == healthVerdictHealthy
	updateProviderHealth(providerName, baseURL, healthy, statusCode, err, "passive")
}

func classifyActiveProbeVerdict(provider config.ProviderConfig, resp *http.Response, err error) providerHealthVerdict {
	if err != nil || resp == nil {
		return healthVerdictUnhealthy
	}

	// For POST (minimal token) probes, 200 is the only reliable health indicator.
	// For GET probes on a specific health path, follow passive logic.
	if strings.TrimSpace(provider.HealthCheckPath) != "" {
		return classifyPassiveProbeVerdict(resp, nil)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return healthVerdictHealthy
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return healthVerdictUnhealthy
	case resp.StatusCode >= 400:
		// If it was a POST probe and failed with 4xx, it's likely unhealthy (auth or model error).
		// If it was a GET probe on the root, it's inconclusive.
		return healthVerdictInconclusive
	default:
		return healthVerdictHealthy
	}
}

func classifyPassiveProbeVerdict(resp *http.Response, err error) providerHealthVerdict {
	if err != nil || resp == nil {
		return healthVerdictUnhealthy
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusRequestTimeout,
		http.StatusTooManyRequests:
		return healthVerdictUnhealthy
	default:
		if resp.StatusCode >= 500 {
			return healthVerdictUnhealthy
		}
		return healthVerdictHealthy
	}
}

func updateProviderHealth(providerName, baseURL string, healthy bool, statusCode int, err error, source string) {
	status := ProviderDependencyStatus{
		Name:       providerName,
		BaseURL:    baseURL,
		Healthy:    healthy,
		StatusCode: statusCode,
		Source:     source,
		UpdatedAt:  time.Now(),
	}
	if err != nil {
		status.LastError = err.Error()
	}

	providerHealthMu.Lock()
	providerHealthMap[providerName] = status
	providerHealthMu.Unlock()
	setProviderHealthMetric(providerName, healthy)

	if healthy {
		logger.Log.Debug(
			"Upstream provider healthy",
			"provider", providerName,
			"source", source,
			"status_code", statusCode,
		)
		return
	}

	logger.Log.Warn(
		"Upstream provider unhealthy",
		"provider", providerName,
		"source", source,
		"status_code", statusCode,
		"last_error", status.LastError,
	)
}

func GetUpstreamStatuses() map[string]ProviderDependencyStatus {
	providerHealthMu.RLock()
	defer providerHealthMu.RUnlock()

	result := make(map[string]ProviderDependencyStatus, len(providerHealthMap))
	for name, status := range providerHealthMap {
		result[name] = status
	}
	return result
}

// GetEffectiveUpstreamStatuses returns the provider dependency view that
// operators should see in readiness output. It overlays circuit breaker state
// on top of the active/passive health snapshot so routing and readiness use the
// same availability truth.
func GetEffectiveUpstreamStatuses() map[string]ProviderDependencyStatus {
	statuses := GetUpstreamStatuses()
	if config.GlobalConfig == nil {
		return statuses
	}

	for _, provider := range config.GlobalConfig.Providers {
		status, ok := statuses[provider.Name]
		if !ok {
			continue
		}

		known, breakerState := circuitBreakerStateForProvider(model.ProviderRoute{
			Name:    provider.Name,
			BaseURL: provider.BaseURL,
		})
		if !known {
			continue
		}

		switch breakerState {
		case gobreaker.StateOpen:
			status.BreakerState = breakerState.String()
			status.Healthy = false
		case gobreaker.StateHalfOpen:
			status.BreakerState = breakerState.String()
		}

		statuses[provider.Name] = status
	}

	return statuses
}

// RunProviderHealthProbeNow forces an immediate active health probe sweep using
// the shared probe client initialized by the gateway proxy. It returns false
// when the probe client has not been initialized yet.
func RunProviderHealthProbeNow() bool {
	client := getProviderProbeClient()
	if client == nil {
		return false
	}
	probeConfiguredProviders(client)
	return true
}

func setProviderProbeClient(client *http.Client) {
	providerProbeClientMu.Lock()
	defer providerProbeClientMu.Unlock()
	providerProbeClient = client
}

func getProviderProbeClient() *http.Client {
	providerProbeClientMu.RLock()
	defer providerProbeClientMu.RUnlock()
	return providerProbeClient
}

func probeConfiguredProviders(client *http.Client) {
	if client == nil || config.GlobalConfig == nil || len(config.GlobalConfig.Providers) == 0 {
		return
	}

	providers := append([]config.ProviderConfig(nil), config.GlobalConfig.Providers...)

	providerProbeMu.Lock()
	defer providerProbeMu.Unlock()

	var wg sync.WaitGroup
	for _, provider := range providers {
		provider := provider
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeProvider(client, provider)
		}()
	}
	wg.Wait()
}

func setProviderHealthMetric(provider string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1
	}
	gatewaymetrics.UpstreamProviderHealth.WithLabelValues(provider).Set(value)
}

func errStatusUnhealthy(statusCode int) error {
	return statusError{statusCode: statusCode}
}

type statusError struct {
	statusCode int
}

func (e statusError) Error() string {
	return http.StatusText(e.statusCode)
}
