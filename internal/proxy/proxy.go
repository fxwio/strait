package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fxwio/strait/internal/adapter"
	"github.com/fxwio/strait/internal/config"
	gatewaymetrics "github.com/fxwio/strait/internal/metrics"
	"github.com/fxwio/strait/internal/middleware"
	"github.com/fxwio/strait/internal/model"
	"github.com/fxwio/strait/internal/response"
	"github.com/fxwio/strait/pkg/logger"
)

var (
	gatewayProxyOnce sync.Once
	gatewayProxy     http.Handler
	baseURLCache     sync.Map // map[string]*url.URL
	activeRequests   int64
)

func GetActiveRequests() int64 {
	return atomic.LoadInt64(&activeRequests)
}

const (
	statusClientClosedRequest = 499
	failureReasonClientCancel = "client_canceled"
	failureReasonTimeout      = "upstream_timeout"
	failureReasonUnavailable  = "upstream_unavailable"
)

type gatewayProxyHandler struct {
	client *http.Client
}

type requestTemplate struct {
	method       string
	requestURL   url.URL
	header       http.Header
	requestID    string
	traceParent  string
	traceState   string
	upstreamBody []byte
}

type compiledUpstreamRequest struct {
	url    url.URL
	host   string
	header http.Header
	body   []byte
}

type upstreamAttemptAction uint8

const (
	upstreamAttemptReturn upstreamAttemptAction = iota
	upstreamAttemptRetrySameProvider
	upstreamAttemptFailoverNextProvider
	upstreamAttemptTerminateRequest
)

type upstreamAttemptDecision struct {
	Action upstreamAttemptAction
	Reason string
}

func NewGatewayProxy() http.Handler {
	gatewayProxyOnce.Do(func() {
		sharedTransport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second, // Reduced from 30s for faster failure detection
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1024,                   // Increased from 512 for better connection reuse
			MaxIdleConnsPerHost:   256,                    // Increased from 128 for better per-host pooling
			MaxConnsPerHost:       0,                      // Changed from 256 to 0 (unlimited) to leverage HTTP/2 multiplexing
			IdleConnTimeout:       60 * time.Second,       // Reduced from 90s for faster connection cleanup
			TLSHandshakeTimeout:   5 * time.Second,        // Reduced from 10s for faster failure detection
			ResponseHeaderTimeout: 30 * time.Second,       // Reduced from 120s for non-streaming requests
			ExpectContinueTimeout: 500 * time.Millisecond, // Reduced from 1s for faster 100-continue handling
		}

		transport := &CircuitBreakerTransport{Transport: sharedTransport}
		initUpstreamHealthMonitor(sharedTransport)

		gatewayProxy = &gatewayProxyHandler{
			client: &http.Client{Transport: transport},
		}
	})

	return gatewayProxy
}

func (h *gatewayProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&activeRequests, 1)
	defer atomic.AddInt64(&activeRequests, -1)

	gatewayCtx, err := getGatewayContext(r)
	if err != nil {
		response.WriteInternalServerError(w, "Gateway context missing.", "missing_gateway_context")
		return
	}

	bodyCtx, ok := middleware.GetRequestBodyContext(r)
	if !ok {
		gatewayCtx.SetTerminalError(http.StatusInternalServerError, response.ErrorTypeServer, "missing_body_context", "missing_body_context")
		response.WriteInternalServerError(w, "Request body context missing.", "missing_body_context")
		return
	}

	if len(gatewayCtx.CandidateProviders) == 0 {
		gatewayCtx.SetTerminalError(http.StatusServiceUnavailable, response.ErrorTypeServer, "no_route_candidates", "no_route_candidates")
		response.WriteServiceUnavailable(w, "No available upstream providers were resolved for this request.", "no_route_candidates")
		return
	}

	var (
		finalErr        error
		totalRetries    int
		lastRetryReason = "unknown"
	)
	requestTemplate := newRequestTemplate(r, bodyCtx.UpstreamBody)
	backoff := retryBackoff()

	for providerIndex, provider := range gatewayCtx.CandidateProviders {
		gatewayCtx.SetActiveProvider(provider)
		adapterProvider := adapter.GetProvider(provider.Name)

		compiledReq, err := requestTemplate.compile(provider, adapterProvider)
		if err != nil {
			gatewayCtx.SetTerminalError(http.StatusInternalServerError, response.ErrorTypeServer, "upstream_request_build_failed", "upstream_request_build_failed")
			response.WriteInternalServerError(w, "Failed to build upstream request.", "upstream_request_build_failed")
			return
		}
		attemptBudget := effectiveRetryCount(provider) + 1

	attemptLoop:
		for attempt := 1; attempt <= attemptBudget; attempt++ {
			attemptStart := time.Now()

			// Apply per-provider, per-tier request timeout.
			timeout := providerTimeout(provider, bodyCtx.IsStream)
			reqCtx, reqCancel := context.WithTimeout(r.Context(), timeout)
			upstreamReq := requestTemplate.newRequest(reqCtx, compiledReq)

			// #nosec G704 -- upstreamReq is constrained to provider base URLs validated from gateway config; client input does not control the destination host.
			resp, err := h.client.Do(upstreamReq)
			markPassiveProbeResult(provider.Name, provider.BaseURL, resp, err)
			attemptDuration := time.Since(attemptStart)
			observeUpstreamAttempt(provider.Name, gatewayCtx.TargetModel, resp, err, attemptDuration, "attempt")
			decision := classifyUpstreamAttempt(r, resp, err)
			attemptReason := decision.Reason

			switch decision.Action {
			case upstreamAttemptRetrySameProvider:
				finalErr = err
				lastRetryReason = attemptReason
				recordUpstreamAttempt(gatewayCtx, provider.Name, providerIndex, attemptBudget, attempt, resp, attemptReason, attemptDuration, retryAttemptResult(attempt, attemptBudget))
				gatewaymetrics.UpstreamRetriesTotal.WithLabelValues(
					provider.Name,
					gatewayCtx.TargetModel,
					lastRetryReason,
				).Inc()

				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				reqCancel()

				totalRetries++
				logRetryAttempt(r, gatewayCtx, provider.Name, providerIndex, attemptBudget, attempt, resp, err)

				if attempt < attemptBudget {
					if !waitRetryBackoff(r.Context(), backoff) {
						finalErr = r.Context().Err()
						lastRetryReason = failureReasonClientCancel
						gatewayCtx.SetTerminalError(statusClientClosedRequest, response.ErrorTypeServer, failureReasonClientCancel, failureReasonClientCancel)
						return
					}
					continue attemptLoop
				}
				break attemptLoop

			case upstreamAttemptFailoverNextProvider:
				lastRetryReason = attemptReason
				recordUpstreamAttempt(gatewayCtx, provider.Name, providerIndex, attemptBudget, attempt, resp, attemptReason, attemptDuration, "failover")
				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				reqCancel()
				break attemptLoop

			case upstreamAttemptTerminateRequest:
				recordUpstreamAttempt(gatewayCtx, provider.Name, providerIndex, attemptBudget, attempt, resp, attemptReason, attemptDuration, "error")
				reqCancel()
				finalErr = err
				lastRetryReason = attemptReason
				gatewayCtx.SetTerminalError(statusClientClosedRequest, response.ErrorTypeServer, failureReasonClientCancel, failureReasonClientCancel)
				logClientCanceled(r, gatewayCtx, provider.Name, providerIndex, attempt, attemptBudget, err)
				return

			case upstreamAttemptReturn:
				recordUpstreamAttempt(gatewayCtx, provider.Name, providerIndex, attemptBudget, attempt, resp, upstreamStatusLabel(resp, nil), attemptDuration, "returned_response")

				w.Header().Set("X-Gateway-Upstream-Provider", provider.Name)
				w.Header().Set("X-Gateway-Upstream-Retries", strconv.Itoa(totalRetries))
				w.Header().Set("X-Gateway-Failovers", strconv.Itoa(gatewayCtx.FailoverCount))

				observeUpstreamAttempt(provider.Name, gatewayCtx.TargetModel, resp, nil, 0, "final_success")

				var writeErr error
				if isStreamingResponse(resp) {
					tw := newTTFTWriter(w, provider.Name, gatewayCtx.TargetModel, attemptStart)
					writeErr = adapterProvider.TranslateResponse(resp, tw, gatewayCtx.TargetModel)
					finishStreamingRequest(r, gatewayCtx, provider.Name, tw, writeErr)
				} else {
					writeErr = adapterProvider.TranslateResponse(resp, w, gatewayCtx.TargetModel)
				}

				if writeErr != nil {
					finalErr = writeErr
					if isClientCanceledRequest(r, writeErr) {
						gatewayCtx.SetTerminalError(statusClientClosedRequest, response.ErrorTypeServer, failureReasonClientCancel, failureReasonClientCancel)
					}
				}
				reqCancel()

				return
			}
		}

		if providerIndex+1 < len(gatewayCtx.CandidateProviders) {
			gatewayCtx.FailoverCount++
			nextProvider := nextProviderName(gatewayCtx.CandidateProviders, providerIndex+1)
			gatewayCtx.RecordFailover(model.UpstreamFailoverTrace{
				FromProvider:  provider.Name,
				ToProvider:    nextProvider,
				ProviderIndex: providerIndex,
				FailoverCount: gatewayCtx.FailoverCount,
				Reason:        lastRetryReason,
			})
			gatewaymetrics.UpstreamFailoversTotal.WithLabelValues(
				provider.Name,
				nextProvider,
				gatewayCtx.TargetModel,
				lastRetryReason,
			).Inc()
			logFailover(r, gatewayCtx, provider.Name, nextProvider, providerIndex, lastRetryReason)
		}
	}

	w.Header().Set("X-Gateway-Upstream-Retries", strconv.Itoa(totalRetries))
	w.Header().Set("X-Gateway-Failovers", strconv.Itoa(gatewayCtx.FailoverCount))

	failure := classifyTerminalProxyFailure(r, finalErr, lastRetryReason)
	gatewayCtx.SetTerminalError(failure.Status, response.ErrorTypeServer, failure.Code, failure.Reason)
	if failure.SuppressResponse {
		return
	}
	switch failure.Status {
	case http.StatusGatewayTimeout:
		response.WriteGatewayTimeout(w, failure.Message, failure.Code)
	default:
		response.WriteServiceUnavailable(w, failure.Message, failure.Code)
	}
}

func newRequestTemplate(orig *http.Request, upstreamBody []byte) requestTemplate {
	requestURL := *orig.URL
	template := requestTemplate{
		method:       orig.Method,
		requestURL:   requestURL,
		header:       orig.Header,
		upstreamBody: upstreamBody,
	}
	if meta, ok := middleware.GetRequestMeta(orig); ok {
		template.requestID = meta.RequestID
		template.traceParent = meta.TraceParent
		template.traceState = meta.TraceState
	}
	return template
}

func (p requestTemplate) compile(provider model.ProviderRoute, adapterProvider adapter.Provider) (compiledUpstreamRequest, error) {
	targetURL, err := parseAndCacheBaseURL(provider.BaseURL)
	if err != nil {
		return compiledUpstreamRequest{}, err
	}

	header := cloneHeader(p.header)
	if p.requestID != "" {
		header.Set("X-Request-ID", p.requestID)
	}
	if p.traceParent != "" {
		header.Set("Traceparent", p.traceParent)
	}
	if p.traceState != "" {
		header.Set("Tracestate", p.traceState)
	}
	header.Del("Accept-Encoding")

	// The adapter receives an owned header map and may mutate it in place.
	path, body, header, err := adapterProvider.CompileRequest(targetURL, p.requestURL.Path, header, p.upstreamBody, provider.APIKey)
	if err != nil {
		return compiledUpstreamRequest{}, err
	}

	requestURL := p.requestURL
	requestURL.Scheme = targetURL.Scheme
	requestURL.Host = targetURL.Host
	requestURL.Path = path
	requestURL.RawPath = requestURL.Path

	compiled := compiledUpstreamRequest{
		url:    requestURL,
		host:   targetURL.Host,
		header: header,
		body:   body,
	}
	return compiled, nil
}

func (p requestTemplate) newRequest(ctx context.Context, compiled compiledUpstreamRequest) *http.Request {
	requestURL := compiled.url
	// net/http Transport preserves request immutability, so the compiled header
	// map can be reused across retry attempts without cloning on the hot path.
	req := &http.Request{
		Method:        p.method,
		URL:           &requestURL,
		Header:        compiled.header,
		Body:          io.NopCloser(bytes.NewReader(compiled.body)),
		Host:          compiled.host,
		ContentLength: int64(len(compiled.body)),
	}
	return req.WithContext(ctx)
}

func waitRetryBackoff(ctx context.Context, backoff time.Duration) bool {
	if backoff <= 0 {
		return true
	}

	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// isStreamingResponse returns true when the response is an SSE stream.
func isStreamingResponse(resp *http.Response) bool {
	ct := resp.Header.Get("Content-Type")
	return strings.Contains(ct, "text/event-stream")
}

func classifyUpstreamAttempt(r *http.Request, resp *http.Response, err error) upstreamAttemptDecision {
	reason := upstreamFailureReason(r, resp, err)

	if isClientCanceledRequest(r, err) {
		return upstreamAttemptDecision{Action: upstreamAttemptTerminateRequest, Reason: reason}
	}
	if err != nil {
		return upstreamAttemptDecision{Action: upstreamAttemptRetrySameProvider, Reason: reason}
	}
	if resp == nil {
		return upstreamAttemptDecision{Action: upstreamAttemptRetrySameProvider, Reason: reason}
	}
	if shouldFailoverStatusCode(resp.StatusCode) {
		return upstreamAttemptDecision{Action: upstreamAttemptFailoverNextProvider, Reason: reason}
	}
	if isRetryableStatusCode(resp.StatusCode) {
		return upstreamAttemptDecision{Action: upstreamAttemptRetrySameProvider, Reason: reason}
	}
	return upstreamAttemptDecision{Action: upstreamAttemptReturn, Reason: reason}
}

func isRetryableStatusCode(statusCode int) bool {
	if config.GlobalConfig == nil {
		return false
	}
	for _, candidate := range config.GlobalConfig.Upstream.RetryableStatusCodes {
		if statusCode == candidate {
			return true
		}
	}
	return false
}

func shouldFailoverStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func effectiveRetryCount(provider model.ProviderRoute) int {
	if provider.MaxRetries > 0 {
		return provider.MaxRetries
	}
	return config.GlobalConfig.Upstream.DefaultMaxRetries
}

func retryBackoff() time.Duration {
	backoff, err := time.ParseDuration(config.GlobalConfig.Upstream.RetryBackoff)
	if err != nil || backoff <= 0 {
		return 200 * time.Millisecond
	}
	return backoff
}

func recordUpstreamAttempt(
	gatewayCtx *model.GatewayContext,
	provider string,
	providerIndex int,
	attemptBudget int,
	attempt int,
	resp *http.Response,
	reason string,
	duration time.Duration,
	result string,
) {
	if gatewayCtx == nil {
		return
	}

	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}

	gatewayCtx.RecordUpstreamAttempt(model.UpstreamAttemptTrace{
		Provider:      provider,
		ProviderIndex: providerIndex,
		Attempt:       attempt,
		AttemptBudget: attemptBudget,
		StatusCode:    statusCode,
		Result:        result,
		Reason:        reason,
		DurationMs:    duration.Milliseconds(),
	})
}

func retryAttemptResult(attempt int, attemptBudget int) string {
	if attempt < attemptBudget {
		return "retry"
	}
	return "retry_exhausted"
}

func logRetryAttempt(r *http.Request, gatewayCtx *model.GatewayContext, provider string, providerIndex int, attemptBudget int, attempt int, resp *http.Response, err error) {
	fields := []any{
		"provider", provider,
		"model", gatewayCtx.TargetModel,
		"provider_index", providerIndex,
		"attempt", attempt,
		"attempt_budget", attemptBudget,
		"failover_count", gatewayCtx.FailoverCount,
		"retry_reason", upstreamFailureReason(r, resp, err),
	}
	if resp != nil {
		fields = append(fields, "status_code", resp.StatusCode)
	}
	if err != nil {
		fields = append(fields, "error", err)
	}
	for _, f := range requestMetaFields(r) {
		fields = append(fields, f)
	}

	logger.Log.Warn("Retrying upstream provider request", fields...)
}

func logClientCanceled(r *http.Request, gatewayCtx *model.GatewayContext, provider string, providerIndex int, attempt int, attemptBudget int, err error) {
	fields := []any{
		"provider", provider,
		"model", gatewayCtx.TargetModel,
		"provider_index", providerIndex,
		"attempt", attempt,
		"attempt_budget", attemptBudget,
		"failure_reason", failureReasonClientCancel,
	}
	if err != nil {
		fields = append(fields, "error", err)
	}
	for _, f := range requestMetaFields(r) {
		fields = append(fields, f)
	}

	logger.Log.Info("Client canceled request before upstream response completed", fields...)
}

func logFailover(r *http.Request, gatewayCtx *model.GatewayContext, provider string, nextProvider string, providerIndex int, reason string) {
	fields := []any{
		"provider", provider,
		"next_provider", nextProvider,
		"model", gatewayCtx.TargetModel,
		"provider_index", providerIndex,
		"failover_count", gatewayCtx.FailoverCount,
		"reason", reason,
		"attempted_providers", gatewayCtx.AttemptedProviders,
	}
	for _, f := range requestMetaFields(r) {
		fields = append(fields, f)
	}

	logger.Log.Warn("Failing over to next upstream provider", fields...)
}

func observeUpstreamAttempt(provider string, modelName string, resp *http.Response, err error, duration time.Duration, result string) {
	statusCode := upstreamStatusLabel(resp, err)
	gatewaymetrics.UpstreamRequestsTotal.WithLabelValues(provider, modelName, statusCode, result).Inc()
	if duration > 0 {
		gatewaymetrics.UpstreamRequestDuration.WithLabelValues(provider, modelName, statusCode).Observe(duration.Seconds())
	}
}

func upstreamFailureReason(r *http.Request, resp *http.Response, err error) string {
	if isClientCanceledRequest(r, err) {
		return failureReasonClientCancel
	}
	if isUpstreamTimeoutError(r, err) {
		return failureReasonTimeout
	}
	if err != nil {
		return "network_error"
	}
	if resp == nil {
		return "no_response"
	}
	return "status_" + strconv.Itoa(resp.StatusCode)
}

type terminalProxyFailure struct {
	Status           int
	Code             string
	Message          string
	Reason           string
	SuppressResponse bool
}

func classifyTerminalProxyFailure(r *http.Request, err error, lastRetryReason string) terminalProxyFailure {
	switch {
	case isClientCanceledRequest(r, err) || lastRetryReason == failureReasonClientCancel:
		return terminalProxyFailure{
			Status:           statusClientClosedRequest,
			Code:             failureReasonClientCancel,
			Reason:           failureReasonClientCancel,
			SuppressResponse: true,
		}
	case lastRetryReason == failureReasonTimeout || isUpstreamTimeoutError(r, err):
		return terminalProxyFailure{
			Status:  http.StatusGatewayTimeout,
			Code:    "upstream_request_timeout",
			Message: "All configured upstream providers timed out.",
			Reason:  failureReasonTimeout,
		}
	default:
		reason := strings.TrimSpace(lastRetryReason)
		if reason == "" || reason == "unknown" {
			reason = failureReasonUnavailable
		}
		return terminalProxyFailure{
			Status:  http.StatusServiceUnavailable,
			Code:    "all_upstreams_unavailable",
			Message: "All configured upstream providers are temporarily unavailable.",
			Reason:  reason,
		}
	}
}

func upstreamStatusLabel(resp *http.Response, err error) string {
	if err != nil {
		return "network_error"
	}
	if resp == nil {
		return "no_response"
	}
	return strconv.Itoa(resp.StatusCode)
}

func isClientCanceledRequest(r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if r != nil && errors.Is(r.Context().Err(), context.Canceled) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "client disconnected")
}

func isUpstreamTimeoutError(r *http.Request, err error) bool {
	if err == nil || isClientCanceledRequest(r, err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func nextProviderName(providers []model.ProviderRoute, nextIndex int) string {
	if nextIndex < 0 || nextIndex >= len(providers) {
		return "none"
	}
	return providers[nextIndex].Name
}

func getGatewayContext(r *http.Request) (*model.GatewayContext, error) {
	gatewayCtx, ok := middleware.GetGatewayContext(r)
	if !ok || gatewayCtx == nil {
		return nil, http.ErrNoCookie
	}
	return gatewayCtx, nil
}

func parseAndCacheBaseURL(raw string) (*url.URL, error) {
	if v, ok := baseURLCache.Load(raw); ok {
		return v.(*url.URL), nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	baseURLCache.Store(raw, parsed)
	return parsed, nil
}

func joinURLPath(basePath, reqPath string) string {
	switch {
	case basePath == "":
		if reqPath == "" {
			return "/"
		}
		return reqPath
	case reqPath == "":
		return basePath
	default:
		return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(reqPath, "/")
	}
}

func requestMetaFields(r *http.Request) []any {
	if meta, ok := middleware.GetRequestMeta(r); ok {
		return []any{
			"request_id", meta.RequestID,
			"trace_id", meta.TraceID,
		}
	}
	return nil
}

func cloneHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for key, values := range src {
		dst[key] = values
	}
	return dst
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if shouldSkipResponseHeader(key) {
			continue
		}
		dst[key] = values
	}
}

func shouldSkipResponseHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func finishStreamingRequest(
	r *http.Request,
	gatewayCtx *model.GatewayContext,
	provider string,
	tw *ttftWriter,
	streamErr error,
) {
	if gatewayCtx == nil || tw == nil {
		return
	}

	outcome := classifyStreamOutcome(r, streamErr, tw.firstChunkSeen)
	gatewayCtx.StreamOutcome = outcome
	gatewayCtx.StreamChunks = tw.chunkCount
	gatewayCtx.StreamBytes = tw.bytesWritten
	tw.RecordMetrics(outcome)

	if streamErr == nil {
		return
	}

	level := slog.LevelWarn
	message := "Streaming request ended with upstream error"
	if outcome == "client_canceled" {
		level = slog.LevelInfo
		message = "Streaming request ended after client disconnect"
	}

	fields := []any{
		"provider", provider,
		"model", gatewayCtx.TargetModel,
		"stream_outcome", outcome,
		"stream_chunks", tw.chunkCount,
		"stream_bytes", tw.bytesWritten,
		"failover_count", gatewayCtx.FailoverCount,
		"upstream_attempt_count", len(gatewayCtx.UpstreamAttempts),
	}
	if streamErr != nil {
		fields = append(fields, "error", streamErr)
	}

	if len(gatewayCtx.UpstreamAttempts) > 0 {
		fields = append(fields, "upstream_attempts", gatewayCtx.UpstreamAttempts)
	}
	if len(gatewayCtx.FailoverEvents) > 0 {
		fields = append(fields, "failover_events", gatewayCtx.FailoverEvents)
	}

	for _, f := range requestMetaFields(r) {
		fields = append(fields, f)
	}

	logger.Log.Log(r.Context(), level, message, fields...)
}

func classifyStreamOutcome(r *http.Request, streamErr error, firstChunkSeen bool) string {
	if streamErr == nil {
		if firstChunkSeen {
			return "completed"
		}
		return "empty"
	}

	if isClientCanceledStream(r, streamErr) {
		return "client_canceled"
	}
	return "upstream_error"
}

func isClientCanceledStream(r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if r != nil {
		if reqErr := r.Context().Err(); errors.Is(reqErr, context.Canceled) || errors.Is(reqErr, context.DeadlineExceeded) {
			return true
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "client disconnected")
}
