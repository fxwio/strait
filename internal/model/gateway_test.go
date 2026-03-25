package model

import "testing"

func TestGatewayContext_SetActiveProvider(t *testing.T) {
	ctx := &GatewayContext{}
	provider := ProviderRoute{
		Name:    "openai",
		BaseURL: "https://api.openai.com",
		APIKey:  "sk-secret",
	}

	ctx.SetActiveProvider(provider)

	if ctx.TargetProvider != "openai" {
		t.Errorf("TargetProvider = %q, want %q", ctx.TargetProvider, "openai")
	}
	if ctx.BaseURL != "https://api.openai.com" {
		t.Errorf("BaseURL = %q, want %q", ctx.BaseURL, "https://api.openai.com")
	}
	if ctx.APIKey != "sk-secret" {
		t.Errorf("APIKey = %q, want %q", ctx.APIKey, "sk-secret")
	}
	if len(ctx.AttemptedProviders) != 1 || ctx.AttemptedProviders[0] != "openai" {
		t.Fatalf("AttemptedProviders = %v, want [openai]", ctx.AttemptedProviders)
	}
}

func TestGatewayContext_SetActiveProvider_OverwritesPrevious(t *testing.T) {
	ctx := &GatewayContext{
		TargetProvider: "old-provider",
		BaseURL:        "https://old.example.com",
		APIKey:         "old-key",
	}

	newProvider := ProviderRoute{
		Name:    "anthropic",
		BaseURL: "https://api.anthropic.com",
		APIKey:  "sk-new-key",
	}
	ctx.SetActiveProvider(newProvider)

	if ctx.TargetProvider != "anthropic" {
		t.Errorf("TargetProvider = %q, want %q", ctx.TargetProvider, "anthropic")
	}
	if ctx.BaseURL != "https://api.anthropic.com" {
		t.Errorf("BaseURL = %q, want %q", ctx.BaseURL, "https://api.anthropic.com")
	}
	if got, want := ctx.AttemptedProviders, []string{"anthropic"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("AttemptedProviders = %v, want %v", got, want)
	}
}

func TestGatewayContext_SetActiveProvider_DoesNotDuplicateProviderOrder(t *testing.T) {
	ctx := &GatewayContext{}
	provider := ProviderRoute{Name: "openai"}

	ctx.SetActiveProvider(provider)
	ctx.SetActiveProvider(provider)

	if got, want := len(ctx.AttemptedProviders), 1; got != want {
		t.Fatalf("len(AttemptedProviders) = %d, want %d", got, want)
	}
}

func TestGatewayContext_RecordTraceData(t *testing.T) {
	ctx := &GatewayContext{}

	ctx.RecordUpstreamAttempt(UpstreamAttemptTrace{
		Provider:      "openai",
		ProviderIndex: 0,
		Attempt:       1,
		AttemptBudget: 2,
		StatusCode:    503,
		Result:        "retry",
		Reason:        "status_503",
		DurationMs:    120,
	})
	ctx.RecordFailover(UpstreamFailoverTrace{
		FromProvider:  "openai",
		ToProvider:    "anthropic",
		ProviderIndex: 0,
		FailoverCount: 1,
		Reason:        "status_503",
	})

	if got, want := len(ctx.UpstreamAttempts), 1; got != want {
		t.Fatalf("len(UpstreamAttempts) = %d, want %d", got, want)
	}
	if got := ctx.UpstreamAttempts[0].Result; got != "retry" {
		t.Fatalf("attempt result = %q, want retry", got)
	}
	if got, want := len(ctx.FailoverEvents), 1; got != want {
		t.Fatalf("len(FailoverEvents) = %d, want %d", got, want)
	}
	if got := ctx.FailoverEvents[0].ToProvider; got != "anthropic" {
		t.Fatalf("failover to provider = %q, want anthropic", got)
	}
}

func TestGatewayContext_SetTerminalError(t *testing.T) {
	ctx := &GatewayContext{}

	ctx.SetTerminalError(504, "server_error", "upstream_request_timeout", "upstream_timeout")

	if ctx.FinalStatusCode != 504 {
		t.Fatalf("FinalStatusCode = %d, want 504", ctx.FinalStatusCode)
	}
	if ctx.FinalErrorType != "server_error" {
		t.Fatalf("FinalErrorType = %q, want server_error", ctx.FinalErrorType)
	}
	if ctx.FinalErrorCode != "upstream_request_timeout" {
		t.Fatalf("FinalErrorCode = %q, want upstream_request_timeout", ctx.FinalErrorCode)
	}
	if ctx.FinalFailureReason != "upstream_timeout" {
		t.Fatalf("FinalFailureReason = %q, want upstream_timeout", ctx.FinalFailureReason)
	}
}
