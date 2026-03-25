package middleware

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	localLimiterBucketTTL       = 10 * time.Minute
	localLimiterCleanupInterval = 1024
)

type keyedLocalLimiter struct {
	mu      sync.Mutex
	buckets map[string]*localTokenBucket
	calls   atomic.Uint64
}

type localTokenBucket struct {
	mu       sync.Mutex
	rate     float64
	burst    float64
	tokens   float64
	last     time.Time
	lastSeen atomic.Int64
}

func newKeyedLocalLimiter() *keyedLocalLimiter {
	return &keyedLocalLimiter{
		buckets: make(map[string]*localTokenBucket),
	}
}

func newLocalTokenBucket(rate float64, burst int, now time.Time) *localTokenBucket {
	bucket := &localTokenBucket{
		rate:   rate,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now,
	}
	bucket.lastSeen.Store(now.UnixNano())
	return bucket
}

func (b *localTokenBucket) Allow(rate float64, burst int, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if rate > 0 {
		b.rate = rate
	}
	if burst > 0 {
		newBurst := float64(burst)
		if newBurst != b.burst {
			if newBurst > b.burst {
				b.tokens = newBurst
			}
			b.burst = newBurst
			if b.tokens > b.burst {
				b.tokens = b.burst
			}
		}
	}

	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.lastSeen.Store(now.UnixNano())

	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}

	if b.tokens < 1 {
		return false
	}

	b.tokens -= 1
	return true
}

func (l *keyedLocalLimiter) Allow(key string, rate float64, burst int) bool {
	now := time.Now()
	l.maybeCleanup(now)

	l.mu.Lock()
	bucket, ok := l.buckets[key]
	if ok && bucketExpired(bucket, now) {
		delete(l.buckets, key)
		bucket = nil
		ok = false
	}
	if !ok {
		bucket = newLocalTokenBucket(rate, burst, now)
		l.buckets[key] = bucket
	}
	l.mu.Unlock()

	return bucket.Allow(rate, burst, now)
}

func (l *keyedLocalLimiter) maybeCleanup(now time.Time) {
	if l.calls.Add(1)%localLimiterCleanupInterval != 0 {
		return
	}

	cutoff := now.Add(-localLimiterBucketTTL).UnixNano()

	l.mu.Lock()
	defer l.mu.Unlock()
	for key, bucket := range l.buckets {
		if bucket.lastSeen.Load() < cutoff {
			delete(l.buckets, key)
		}
	}
}

func bucketExpired(bucket *localTokenBucket, now time.Time) bool {
	if bucket == nil {
		return true
	}
	lastSeen := bucket.lastSeen.Load()
	if lastSeen == 0 {
		return false
	}
	return now.Sub(time.Unix(0, lastSeen)) > localLimiterBucketTTL
}
