package router

import (
	"net/http"
	"testing"
	"time"

	"github.com/fxwio/strait/internal/proxy"
)

func TestBuildReadyHealthResponse_UnhealthyWhenAllProvidersDown(t *testing.T) {
	resp, statusCode := buildReadyHealthResponse(
		map[string]proxy.ProviderDependencyStatus{
			"openai":    {Name: "openai", Healthy: false},
			"anthropic": {Name: "anthropic", Healthy: false},
		},
	)

	if statusCode != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503", statusCode)
	}
	if resp.Status != "unhealthy" {
		t.Fatalf("status = %q, want unhealthy", resp.Status)
	}
	assertContains(t, resp.DegradedFeatures, "all_providers_down")
}

func TestBuildReadyHealthResponse_DegradedWhenProviderHealthUnverified(t *testing.T) {
	resp, statusCode := buildReadyHealthResponse(
		map[string]proxy.ProviderDependencyStatus{
			"openai": {
				Name:      "openai",
				Healthy:   true,
				Source:    "bootstrap",
				UpdatedAt: time.Unix(1700000000, 0),
			},
			"anthropic": {
				Name:      "anthropic",
				Healthy:   true,
				Source:    "bootstrap",
				UpdatedAt: time.Unix(1700000001, 0),
			},
		},
	)

	if statusCode != http.StatusOK {
		t.Fatalf("status code = %d, want 200", statusCode)
	}
	if resp.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", resp.Status)
	}
	assertContains(t, resp.DegradedFeatures, "provider_health_unverified")
}

func TestBuildReadyHealthResponse_HealthyWhenAtLeastOneProviderVerifiedHealthy(t *testing.T) {
	resp, statusCode := buildReadyHealthResponse(
		map[string]proxy.ProviderDependencyStatus{
			"openai": {
				Name:      "openai",
				Healthy:   true,
				Source:    "active",
				UpdatedAt: time.Unix(1700000000, 0),
			},
			"anthropic": {
				Name:      "anthropic",
				Healthy:   true,
				Source:    "bootstrap",
				UpdatedAt: time.Unix(1700000001, 0),
			},
		},
	)

	if statusCode != http.StatusOK {
		t.Fatalf("status code = %d, want 200", statusCode)
	}
	if resp.Status != "ok" {
		t.Fatalf("status = %q, want ok", resp.Status)
	}
	assertNotContains(t, resp.DegradedFeatures, "provider_health_unverified")
}

func assertContains(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if item == want {
			return
		}
	}
	t.Fatalf("missing %q in %v", want, items)
}

func assertNotContains(t *testing.T, items []string, unwanted string) {
	t.Helper()
	for _, item := range items {
		if item == unwanted {
			t.Fatalf("did not expect %q in %v", unwanted, items)
		}
	}
}
