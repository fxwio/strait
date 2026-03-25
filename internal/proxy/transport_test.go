package proxy

import (
	"sync"
	"testing"

	"github.com/fxwio/strait/internal/config"
	"github.com/sony/gobreaker"
)

func TestGetBreaker_ReusesInstanceForSameKey(t *testing.T) {
	resetBreakersForTest()
	config.GlobalConfig = breakerConfigForTest()

	first := getBreaker("openai@api.openai.com")
	second := getBreaker("openai@api.openai.com")

	if first == nil || second == nil {
		t.Fatal("expected breaker instance")
	}
	if first != second {
		t.Fatal("expected same breaker instance for identical key")
	}
}

func TestGetBreaker_ConcurrentSameKeyInitializesOnce(t *testing.T) {
	resetBreakersForTest()
	config.GlobalConfig = breakerConfigForTest()

	const goroutines = 32
	results := make(chan *gobreaker.CircuitBreaker, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- getBreaker("anthropic@api.anthropic.com")
		}()
	}
	wg.Wait()
	close(results)

	var first *gobreaker.CircuitBreaker
	for breaker := range results {
		if breaker == nil {
			t.Fatal("expected breaker instance")
		}
		if first == nil {
			first = breaker
			continue
		}
		if breaker != first {
			t.Fatal("expected all goroutines to observe the same breaker instance")
		}
	}
}

func BenchmarkGetBreaker_Parallel(b *testing.B) {
	resetBreakersForTest()
	config.GlobalConfig = breakerConfigForTest()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if getBreaker("openai@api.openai.com") == nil {
				b.Fatal("expected breaker instance")
			}
		}
	})
}

func breakerConfigForTest() *config.Config {
	return &config.Config{
		Upstream: config.UpstreamConfig{
			BreakerInterval:         "10s",
			BreakerTimeout:          "15s",
			BreakerFailureRatio:     0.5,
			BreakerMinimumRequests:  5,
			BreakerHalfOpenRequests: 1,
		},
	}
}

func resetBreakersForTest() {
	cbMu.Lock()
	defer cbMu.Unlock()
	cbMap = make(map[string]*gobreaker.CircuitBreaker)
}

func snapshotBreakersForTest() map[string]*gobreaker.CircuitBreaker {
	cbMu.RLock()
	defer cbMu.RUnlock()

	snapshot := make(map[string]*gobreaker.CircuitBreaker, len(cbMap))
	for key, breaker := range cbMap {
		snapshot[key] = breaker
	}
	return snapshot
}

func restoreBreakersForTest(snapshot map[string]*gobreaker.CircuitBreaker) {
	resetBreakersForTest()
	cbMu.Lock()
	defer cbMu.Unlock()
	for key, breaker := range snapshot {
		cbMap[key] = breaker
	}
}
