package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyUpstreamAttempt_ClientCanceledTerminates(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	decision := classifyUpstreamAttempt(req, nil, context.Canceled)
	if decision.Action != upstreamAttemptTerminateRequest {
		t.Fatalf("action = %v, want terminate_request", decision.Action)
	}
}

func TestClassifyUpstreamAttempt_UpstreamTimeoutRetriesSameProvider(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	decision := classifyUpstreamAttempt(req, nil, context.DeadlineExceeded)
	if decision.Action != upstreamAttemptRetrySameProvider {
		t.Fatalf("action = %v, want retry_same_provider", decision.Action)
	}
}

func TestClassifyUpstreamAttempt_ProviderAuthFailureFailsOver(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{StatusCode: http.StatusUnauthorized}

	decision := classifyUpstreamAttempt(req, resp, nil)
	if decision.Action != upstreamAttemptFailoverNextProvider {
		t.Fatalf("action = %v, want failover_next_provider", decision.Action)
	}
}

func TestClassifyUpstreamAttempt_ProviderRateLimitFailsOver(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests}

	decision := classifyUpstreamAttempt(req, resp, nil)
	if decision.Action != upstreamAttemptFailoverNextProvider {
		t.Fatalf("action = %v, want failover_next_provider", decision.Action)
	}
}

func TestClassifyUpstreamAttempt_ClientErrorReturnsResponse(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{StatusCode: http.StatusBadRequest}

	decision := classifyUpstreamAttempt(req, resp, nil)
	if decision.Action != upstreamAttemptReturn {
		t.Fatalf("action = %v, want return_response", decision.Action)
	}
}

func TestUpstreamFailureReason_ClassifiesTimeout(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	if got := upstreamFailureReason(req, nil, context.DeadlineExceeded); got != failureReasonTimeout {
		t.Fatalf("reason = %q, want %q", got, failureReasonTimeout)
	}
}

func TestClassifyTerminalProxyFailure_ClientCanceled(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	failure := classifyTerminalProxyFailure(req, context.Canceled, failureReasonClientCancel)
	if failure.Status != statusClientClosedRequest {
		t.Fatalf("status = %d, want %d", failure.Status, statusClientClosedRequest)
	}
	if failure.Code != failureReasonClientCancel {
		t.Fatalf("code = %q, want %q", failure.Code, failureReasonClientCancel)
	}
	if !failure.SuppressResponse {
		t.Fatal("expected client-canceled failure to suppress response writing")
	}
}

func TestClassifyTerminalProxyFailure_Timeout(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	failure := classifyTerminalProxyFailure(req, context.DeadlineExceeded, failureReasonTimeout)
	if failure.Status != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", failure.Status, http.StatusGatewayTimeout)
	}
	if failure.Code != "upstream_request_timeout" {
		t.Fatalf("code = %q, want upstream_request_timeout", failure.Code)
	}
	if failure.Reason != failureReasonTimeout {
		t.Fatalf("reason = %q, want %q", failure.Reason, failureReasonTimeout)
	}
}

func TestClassifyTerminalProxyFailure_Unavailable(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	failure := classifyTerminalProxyFailure(req, errors.New("dial tcp: connection refused"), "status_503")
	if failure.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", failure.Status, http.StatusServiceUnavailable)
	}
	if failure.Code != "all_upstreams_unavailable" {
		t.Fatalf("code = %q, want all_upstreams_unavailable", failure.Code)
	}
	if failure.Reason != "status_503" {
		t.Fatalf("reason = %q, want status_503", failure.Reason)
	}
}
