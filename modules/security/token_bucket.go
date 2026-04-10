package security

import (
	"sync"
	"time"
)

type TokenBucket struct {
	tokens        int64
	maxTokens     int64
	refillRate    int64         // tokens per window
	window        time.Duration // time window for refill
	lastRefill    time.Time
	mu            sync.Mutex
}

func NewTokenBucket(maxTokens int64, refillRate int64, window time.Duration) *TokenBucket {
	return &TokenBucket{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		window:     window,
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)

	// Refill
	if elapsed >= tb.window {
		// Calculate how many windows have passed
		windows := int64(elapsed / tb.window)
		tb.tokens += windows * tb.refillRate
		if tb.tokens > tb.maxTokens {
			tb.tokens = tb.maxTokens
		}
		// Advance lastRefill by the exact number of windows
		tb.lastRefill = tb.lastRefill.Add(time.Duration(windows) * tb.window)
	}

	if tb.tokens > 0 {
		tb.tokens--
		return true
	}

	return false
}

type RateLimiter struct {
	buckets    map[string]*TokenBucket
	maxTokens  int64
	refillRate int64
	window     time.Duration
	mu         sync.RWMutex
}

func NewRateLimiter(maxTokens int64, refillRate int64, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		buckets:    make(map[string]*TokenBucket),
		maxTokens:  maxTokens,
		refillRate: refillRate,
		window:     window,
	}

	// Simple cleanup routine
	go func() {
		for {
			time.Sleep(window * 10)
			rl.cleanup()
		}
	}()

	return rl
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, bucket := range rl.buckets {
		bucket.mu.Lock()
		elapsed := now.Sub(bucket.lastRefill)
		bucket.mu.Unlock()

		if elapsed > rl.window*10 {
			delete(rl.buckets, ip)
		}
	}
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.RLock()
	bucket, exists := rl.buckets[ip]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		bucket, exists = rl.buckets[ip]
		if !exists {
			bucket = NewTokenBucket(rl.maxTokens, rl.refillRate, rl.window)
			rl.buckets[ip] = bucket
		}
		rl.mu.Unlock()
	}

	return bucket.Allow()
}
