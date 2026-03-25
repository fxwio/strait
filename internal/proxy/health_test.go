package proxy

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/model"
	"github.com/sony/gobreaker"
)

func TestRefreshProviderHealthSnapshot_PreservesExistingAddsNewAndRemovesStale(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", BaseURL: "https://openai.example.com"},
			{Name: "anthropic", BaseURL: "https://anthropic.example.com"},
		},
	}

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{
		"openai": {
			Name:       "openai",
			BaseURL:    "https://old-openai.example.com",
			Healthy:    false,
			StatusCode: 503,
			LastError:  "Service Unavailable",
			Source:     "passive",
			UpdatedAt:  time.Unix(1700000000, 0),
		},
		"legacy": {
			Name:      "legacy",
			BaseURL:   "https://legacy.example.com",
			Healthy:   false,
			Source:    "active",
			UpdatedAt: time.Unix(1700000001, 0),
		},
	}
	providerHealthMu.Unlock()

	RefreshProviderHealthSnapshot()

	statuses := GetUpstreamStatuses()
	if len(statuses) != 2 {
		t.Fatalf("len(statuses) = %d, want 2", len(statuses))
	}

	openai, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if openai.Healthy {
		t.Fatal("expected existing openai unhealthy status to be preserved")
	}
	if openai.StatusCode != 503 || openai.LastError != "Service Unavailable" || openai.Source != "passive" {
		t.Fatalf("openai status = %+v, want preserved unhealthy status", openai)
	}
	if openai.BaseURL != "https://openai.example.com" {
		t.Fatalf("openai BaseURL = %q, want updated config URL", openai.BaseURL)
	}

	anthropic, ok := statuses["anthropic"]
	if !ok {
		t.Fatal("expected anthropic status")
	}
	if !anthropic.Healthy || anthropic.Source != "bootstrap" {
		t.Fatalf("anthropic status = %+v, want bootstrap healthy", anthropic)
	}
	if anthropic.BaseURL != "https://anthropic.example.com" {
		t.Fatalf("anthropic BaseURL = %q, want config URL", anthropic.BaseURL)
	}

	if _, ok := statuses["legacy"]; ok {
		t.Fatal("did not expect legacy provider to remain after refresh")
	}
}

func TestMarkPassiveProbeResult_ProviderAuthFailureMarksUnhealthy(t *testing.T) {
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{}
	providerHealthMu.Unlock()

	markPassiveProbeResult(
		"openai",
		"https://openai.example.com",
		&http.Response{StatusCode: http.StatusUnauthorized},
		nil,
	)

	statuses := GetUpstreamStatuses()
	openai, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if openai.Healthy {
		t.Fatalf("status = %+v, want unhealthy", openai)
	}
	if openai.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status_code = %d, want %d", openai.StatusCode, http.StatusUnauthorized)
	}
}

func TestMarkPassiveProbeResult_ProviderRateLimitMarksUnhealthy(t *testing.T) {
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{}
	providerHealthMu.Unlock()

	markPassiveProbeResult(
		"openai",
		"https://openai.example.com",
		&http.Response{StatusCode: http.StatusTooManyRequests},
		nil,
	)

	statuses := GetUpstreamStatuses()
	openai, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if openai.Healthy {
		t.Fatalf("status = %+v, want unhealthy", openai)
	}
	if openai.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status_code = %d, want %d", openai.StatusCode, http.StatusTooManyRequests)
	}
}

func TestMarkPassiveProbeResult_ClientBadRequestStaysHealthy(t *testing.T) {
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{}
	providerHealthMu.Unlock()

	markPassiveProbeResult(
		"openai",
		"https://openai.example.com",
		&http.Response{StatusCode: http.StatusBadRequest},
		nil,
	)

	statuses := GetUpstreamStatuses()
	openai, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if !openai.Healthy {
		t.Fatalf("status = %+v, want healthy", openai)
	}
	if openai.StatusCode != http.StatusBadRequest {
		t.Fatalf("status_code = %d, want %d", openai.StatusCode, http.StatusBadRequest)
	}
}

func TestProbeProvider_WithoutHealthCheckPath_4xxIsInconclusive(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	config.GlobalConfig = &config.Config{
		Upstream: config.UpstreamConfig{
			HealthCheckTimeout: "1s",
		},
	}

	provider := config.ProviderConfig{
		Name:    "openai",
		BaseURL: "https://openai.example.com",
	}

	seed := ProviderDependencyStatus{
		Name:      "openai",
		BaseURL:   provider.BaseURL,
		Healthy:   false,
		Source:    "passive",
		UpdatedAt: time.Unix(1700000000, 0),
	}
	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{
		"openai": seed,
	}
	providerHealthMu.Unlock()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		}),
	}

	probeProvider(client, provider)

	statuses := GetUpstreamStatuses()
	got, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if got != seed {
		t.Fatalf("status = %+v, want unchanged %+v", got, seed)
	}
}

func TestProbeProvider_WithHealthCheckPath_4xxMarksUnhealthy(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()
	})

	config.GlobalConfig = &config.Config{
		Upstream: config.UpstreamConfig{
			HealthCheckTimeout: "1s",
		},
	}

	provider := config.ProviderConfig{
		Name:            "openai",
		BaseURL:         "https://openai.example.com",
		HealthCheckPath: "/healthz",
	}

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{}
	providerHealthMu.Unlock()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/healthz" {
				t.Fatalf("path = %q, want /healthz", req.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		}),
	}

	probeProvider(client, provider)

	statuses := GetUpstreamStatuses()
	openai, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if openai.Healthy {
		t.Fatalf("status = %+v, want unhealthy", openai)
	}
	if openai.StatusCode != http.StatusNotFound {
		t.Fatalf("status_code = %d, want %d", openai.StatusCode, http.StatusNotFound)
	}
	if openai.Source != "active" {
		t.Fatalf("source = %q, want active", openai.Source)
	}
}

func TestResolveProviderHealth_CircuitOpenOverridesHealthySnapshot(t *testing.T) {
	previousStatuses := GetUpstreamStatuses()
	cbMu.RLock()
	previousBreakers := make(map[string]*gobreaker.CircuitBreaker, len(cbMap))
	for key, cb := range cbMap {
		previousBreakers[key] = cb
	}
	cbMu.RUnlock()

	t.Cleanup(func() {
		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()

		cbMu.Lock()
		cbMap = previousBreakers
		cbMu.Unlock()
	})

	candidate := model.ProviderRoute{
		Name:    "openai",
		BaseURL: "https://api.openai.com",
	}

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{
		"openai": {
			Name:      "openai",
			BaseURL:   candidate.BaseURL,
			Healthy:   true,
			Source:    "passive",
			UpdatedAt: time.Unix(1700000000, 0),
		},
	}
	providerHealthMu.Unlock()

	breakerKey := breakerKeyForProvider(candidate)
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: breakerKey,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return true
		},
	})
	_, _ = cb.Execute(func() (interface{}, error) {
		return nil, errors.New("boom")
	})
	if cb.State() != gobreaker.StateOpen {
		t.Fatalf("breaker state = %s, want %s", cb.State().String(), gobreaker.StateOpen.String())
	}

	cbMu.Lock()
	cbMap = map[string]*gobreaker.CircuitBreaker{
		breakerKey: cb,
	}
	cbMu.Unlock()

	known, healthy := resolveProviderHealth(candidate)
	if !known {
		t.Fatal("expected provider health to be known")
	}
	if healthy {
		t.Fatal("expected open circuit breaker to mark candidate unhealthy")
	}
}

func TestGetEffectiveUpstreamStatuses_OverlaysCircuitBreakerState(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	cbMu.RLock()
	previousBreakers := make(map[string]*gobreaker.CircuitBreaker, len(cbMap))
	for key, cb := range cbMap {
		previousBreakers[key] = cb
	}
	cbMu.RUnlock()

	t.Cleanup(func() {
		config.GlobalConfig = previousConfig

		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()

		cbMu.Lock()
		cbMap = previousBreakers
		cbMu.Unlock()
	})

	config.GlobalConfig = &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", BaseURL: "https://api.openai.com"},
		},
	}

	providerHealthMu.Lock()
	providerHealthMap = map[string]ProviderDependencyStatus{
		"openai": {
			Name:      "openai",
			BaseURL:   "https://api.openai.com",
			Healthy:   true,
			Source:    "passive",
			UpdatedAt: time.Unix(1700000000, 0),
		},
	}
	providerHealthMu.Unlock()

	candidate := model.ProviderRoute{Name: "openai", BaseURL: "https://api.openai.com"}
	breakerKey := breakerKeyForProvider(candidate)
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: breakerKey,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return true
		},
	})
	_, _ = cb.Execute(func() (interface{}, error) {
		return nil, errors.New("boom")
	})
	if cb.State() != gobreaker.StateOpen {
		t.Fatalf("breaker state = %s, want %s", cb.State().String(), gobreaker.StateOpen.String())
	}

	cbMu.Lock()
	cbMap = map[string]*gobreaker.CircuitBreaker{
		breakerKey: cb,
	}
	cbMu.Unlock()

	statuses := GetEffectiveUpstreamStatuses()
	openai, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if openai.Healthy {
		t.Fatalf("status = %+v, want unhealthy", openai)
	}
	if openai.BreakerState != gobreaker.StateOpen.String() {
		t.Fatalf("breaker_state = %q, want %q", openai.BreakerState, gobreaker.StateOpen.String())
	}
}

func TestRunProviderHealthProbeNow_ProbesConfiguredProviders(t *testing.T) {
	previousConfig := config.GlobalConfig
	previousStatuses := GetUpstreamStatuses()
	previousProbeClient := getProviderProbeClient()
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig

		providerHealthMu.Lock()
		providerHealthMap = previousStatuses
		providerHealthMu.Unlock()

		setProviderProbeClient(previousProbeClient)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	config.GlobalConfig = &config.Config{
		Upstream: config.UpstreamConfig{
			HealthCheckTimeout: "1s",
		},
		Providers: []config.ProviderConfig{
			{
				Name:            "openai",
				BaseURL:         server.URL,
				HealthCheckPath: "/healthz",
			},
		},
	}

	RefreshProviderHealthSnapshot()
	setProviderProbeClient(server.Client())

	if ok := RunProviderHealthProbeNow(); !ok {
		t.Fatal("expected probe run to be triggered")
	}

	statuses := GetUpstreamStatuses()
	openai, ok := statuses["openai"]
	if !ok {
		t.Fatal("expected openai status")
	}
	if !openai.Healthy {
		t.Fatalf("status = %+v, want healthy", openai)
	}
	if openai.Source != "active" {
		t.Fatalf("source = %q, want active", openai.Source)
	}
	if openai.StatusCode != http.StatusNoContent {
		t.Fatalf("status_code = %d, want %d", openai.StatusCode, http.StatusNoContent)
	}
}
