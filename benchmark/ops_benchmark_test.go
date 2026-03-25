package benchmark

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/fxwio/strait/internal/config"
	"github.com/fxwio/strait/internal/router"
	"github.com/fxwio/strait/pkg/logger"
)

const (
	benchmarkOpenAIModel    = "gpt-4o-mini"
	benchmarkAnthropicModel = "claude-3-5-sonnet"
	benchmarkToken          = "bench-token"
	benchmarkAuthHeader     = "Bearer " + benchmarkToken
	benchmarkRequestID      = "req_benchmark"
	benchmarkTraceParent    = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)

var (
	benchmarkSetupOnce sync.Once
	benchmarkFixture   *fixture
	benchmarkSetupErr  error
)

type fixture struct {
	router           http.Handler
	chatPath         string
	authHeader       string
	openAIBody       []byte
	anthropicBody    []byte
	streamBody       []byte
	missingModelBody []byte
}

func BenchmarkHealthLive(b *testing.B) {
	fx := mustBenchmarkFixture(b)
	req := newBenchmarkRequest(http.MethodGet, "/health/live", nil, "")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
		}
	}
}

func BenchmarkChatCompletions_MissingAuth(b *testing.B) {
	fx := mustBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchmarkRequest(http.MethodPost, fx.chatPath, fx.openAIBody, "")
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			b.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusUnauthorized)
		}
	}
}

func BenchmarkChatCompletions_ModelNotFound(b *testing.B) {
	fx := mustBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchmarkRequest(http.MethodPost, fx.chatPath, fx.missingModelBody, fx.authHeader)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			b.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusNotFound)
		}
	}
}

func BenchmarkChatCompletions_OpenAI_ProxyOK(b *testing.B) {
	fx := mustBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchmarkRequest(http.MethodPost, fx.chatPath, fx.openAIBody, fx.authHeader)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
		}
	}
}

func BenchmarkChatCompletions_Anthropic_ProxyOK(b *testing.B) {
	fx := mustBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchmarkRequest(http.MethodPost, fx.chatPath, fx.anthropicBody, fx.authHeader)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
		}
	}
}

func BenchmarkChatCompletions_StreamProxyOK(b *testing.B) {
	fx := mustBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchmarkRequest(http.MethodPost, fx.chatPath, fx.streamBody, fx.authHeader)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
		}
	}
}

func BenchmarkChatCompletions_Parallel_ProxyOK(b *testing.B) {
	fx := mustBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := newBenchmarkRequest(http.MethodPost, fx.chatPath, fx.openAIBody, fx.authHeader)
			rr := httptest.NewRecorder()
			fx.router.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status code: got=%d want=%d", rr.Code, http.StatusOK)
			}
		}
	})
}

func mustBenchmarkFixture(b *testing.B) *fixture {
	b.Helper()

	benchmarkSetupOnce.Do(func() {
		// Silence logger during benchmarks to avoid terminal flooding and IO bottlenecks.
		logger.Log = slog.New(slog.NewJSONHandler(io.Discard, nil))
		benchmarkFixture, benchmarkSetupErr = newBenchmarkFixture()
	})
	if benchmarkSetupErr != nil {
		b.Fatalf("benchmark setup failed: %v", benchmarkSetupErr)
	}
	return benchmarkFixture
}

func newBenchmarkFixture() (*fixture, error) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusNoContent)
			return
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte(`"stream":true`)) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-bench\",\"object\":\"chat.completion.chunk\",\"created\":1710000000,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
				_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-bench\",\"object\":\"chat.completion.chunk\",\"created\":1710000000,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"chatcmpl-bench","object":"chat.completion","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`)
			return
		case "/v1/messages": // Anthropic endpoint
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"msg_bench","type":"message","role":"assistant","model":"claude-3-5-sonnet","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":4}}`)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}))

	config.GlobalConfig = &config.Config{
		Auth: config.AuthConfig{
			RateLimitQPS:   1000000000,
			RateLimitBurst: 1000000000,
			Tokens: []config.ClientTokenConfig{
				{
					Name:     "benchmark",
					Value:    benchmarkToken,
					Disabled: false,
				},
			},
		},
		Upstream: config.UpstreamConfig{
			DefaultTimeoutNonStream: "5s",
			DefaultTimeoutStream:    "15s",
			HealthCheckInterval:     "24h",
		},
		Providers: []config.ProviderConfig{
			{
				Name:            "openai",
				BaseURL:         upstream.URL,
				Models:          []string{benchmarkOpenAIModel},
				HealthCheckPath: "healthz",
			},
			{
				Name:            "anthropic",
				BaseURL:         upstream.URL,
				Models:          []string{benchmarkAnthropicModel},
				HealthCheckPath: "healthz",
			},
		},
	}

	return &fixture{
		router:           router.NewRouter(),
		chatPath:         "/v1/chat/completions",
		authHeader:       benchmarkAuthHeader,
		openAIBody:       []byte(`{"model":"` + benchmarkOpenAIModel + `","messages":[{"role":"user","content":"hello"}]}`),
		anthropicBody:    []byte(`{"model":"` + benchmarkAnthropicModel + `","messages":[{"role":"user","content":"hello"}]}`),
		streamBody:       []byte(`{"model":"` + benchmarkOpenAIModel + `","stream":true,"messages":[{"role":"user","content":"hello"}]}`),
		missingModelBody: []byte(`{"model":"missing-model","messages":[{"role":"user","content":"hello"}]}`),
	}, nil
}

func newBenchmarkRequest(method, target string, body []byte, authHeader string) *http.Request {
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, bodyReader)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Request-ID", benchmarkRequestID)
	req.Header.Set("Traceparent", benchmarkTraceParent)
	req.Header.Set("User-Agent", "strait-benchmark")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}
