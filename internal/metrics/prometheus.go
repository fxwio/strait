package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RequestTotal 统计网关入口请求总量，按 provider / model / status_code / cache_status 维度聚合。
	RequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of HTTP requests completed by the AI gateway.",
		},
		[]string{"provider", "model", "status_code", "cache_status"},
	)

	// RequestDuration 统计网关入口请求总时延，覆盖缓存命中、短请求与长推理场景。
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Histogram of completed HTTP request latencies in seconds.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		},
		[]string{"provider", "model", "status_code", "cache_status"},
	)

	// RequestsInFlight 统计当前正在处理的请求数，仅保留 route 维度避免高基数标签爆炸。
	RequestsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_requests_in_flight",
			Help: "Current number of in-flight HTTP requests being processed by the AI gateway.",
		},
		[]string{"route"},
	)

	// CacheRequestsTotal 统计缓存命中情况。
	CacheRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_cache_requests_total",
			Help: "Total number of cache lookups by result.",
		},
		[]string{"provider", "model", "result"},
	)

	// UpstreamRequestsTotal 统计每一次上游尝试，而不是仅统计最终网关响应。
	UpstreamRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_upstream_requests_total",
			Help: "Total number of upstream provider attempts made by the gateway.",
		},
		[]string{"provider", "model", "status_code", "result"},
	)

	// UpstreamRequestDuration 统计单次上游尝试时延。
	UpstreamRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_upstream_request_duration_seconds",
			Help:    "Histogram of upstream provider request latencies in seconds.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		},
		[]string{"provider", "model", "status_code"},
	)

	// UpstreamRetriesTotal 统计可重试错误触发次数。
	UpstreamRetriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_upstream_retries_total",
			Help: "Total number of retryable upstream failures observed by the gateway.",
		},
		[]string{"provider", "model", "reason"},
	)

	// UpstreamFailoversTotal 统计 provider 之间的故障切换。
	UpstreamFailoversTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_upstream_failovers_total",
			Help: "Total number of provider failovers performed by the gateway.",
		},
		[]string{"from_provider", "to_provider", "model", "reason"},
	)

	// UpstreamProviderHealth 暴露每个 provider 最近一次健康状态，1 为健康，0 为不健康。
	UpstreamProviderHealth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_upstream_provider_health",
			Help: "Health state of upstream providers observed by active/passive checks. 1 means healthy.",
		},
		[]string{"provider"},
	)

	// CircuitBreakerState 暴露熔断器状态：0=closed, 1=half_open, 2=open。
	CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_circuit_breaker_state",
			Help: "Current circuit breaker state per breaker key. 0=closed, 1=half_open, 2=open.",
		},
		[]string{"breaker"},
	)

	// StreamTTFT measures Time-to-First-Token for streaming responses.
	// TTFT is measured from when the upstream request is sent to when the
	// first response byte (SSE chunk) is written back to the client.
	// This is the primary user-perceived latency metric for streaming LLM calls.
	StreamTTFT = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_stream_ttft_seconds",
			Help:    "Time-to-first-token for streaming LLM responses, in seconds.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		},
		[]string{"provider", "model"},
	)

	// StreamDuration measures total wall-clock time from the first upstream
	// request attempt to the last byte written to the client for streaming responses.
	StreamDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_stream_duration_seconds",
			Help:    "Total duration of streaming LLM responses from first upstream attempt to last byte, in seconds.",
			Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		},
		[]string{"provider", "model"},
	)

	// StreamTerminationsTotal counts how streaming requests finished.
	// This helps operators separate healthy completions from client disconnects
	// and upstream stream failures.
	StreamTerminationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_stream_terminations_total",
			Help: "Total number of streaming responses by termination outcome.",
		},
		[]string{"provider", "model", "outcome"},
	)

	// IPRateLimitRejectedTotal counts requests rejected by the pre-auth IP rate
	// limiter (UnauthIPRateLimitMiddleware). A sustained climb signals a flood
	// or misconfigured client that warrants investigation.
	IPRateLimitRejectedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_ip_rate_limit_rejected_total",
			Help: "Total number of requests rejected by the pre-auth per-IP rate limiter.",
		},
	)
)
