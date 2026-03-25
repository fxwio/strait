package middleware

import (
	"strconv"
	"testing"
	"time"
)

func TestKeyedLocalLimiter_ExpiresIdleBucket(t *testing.T) {
	limiter := newKeyedLocalLimiter()
	key := "rate_limit:token:test"

	if !limiter.Allow(key, 1, 2) {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow(key, 1, 2) {
		t.Fatal("second request should be allowed")
	}
	if limiter.Allow(key, 1, 2) {
		t.Fatal("third request should be rejected after burst exhaustion")
	}

	limiter.mu.Lock()
	bucket := limiter.buckets[key]
	limiter.mu.Unlock()
	if bucket == nil {
		t.Fatal("expected bucket to exist")
	}

	bucket.lastSeen.Store(time.Now().Add(-localLimiterBucketTTL - time.Second).UnixNano())

	if !limiter.Allow(key, 1, 2) {
		t.Fatal("expired bucket should be recreated with fresh tokens")
	}
	if !limiter.Allow(key, 1, 2) {
		t.Fatal("recreated bucket should allow the second burst token")
	}
	if limiter.Allow(key, 1, 2) {
		t.Fatal("recreated bucket should reject after burst exhaustion")
	}
}

func TestKeyedLocalLimiter_UpdatesBurstForExistingBucket(t *testing.T) {
	limiter := newKeyedLocalLimiter()
	key := "rate_limit:ip:127.0.0.1"

	if !limiter.Allow(key, 1, 1) {
		t.Fatal("first request should be allowed")
	}
	if limiter.Allow(key, 1, 1) {
		t.Fatal("second request should be rejected with burst=1")
	}

	if !limiter.Allow(key, 1000, 3) {
		t.Fatal("bucket should adapt to larger burst")
	}
	if !limiter.Allow(key, 1000, 3) {
		t.Fatal("bucket should allow second token after burst increase")
	}
	if !limiter.Allow(key, 1000, 3) {
		t.Fatal("bucket should allow third token after burst increase")
	}
	if limiter.Allow(key, 1000, 3) {
		t.Fatal("bucket should reject after new burst exhaustion")
	}
}

func BenchmarkKeyedLocalLimiter_AllowParallel(b *testing.B) {
	limiter := newKeyedLocalLimiter()
	keys := make([]string, 256)
	for i := range keys {
		keys[i] = "rate_limit:token:" + strconv.Itoa(i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = limiter.Allow(keys[i&255], 1000000, 1000000)
			i++
		}
	})
}
