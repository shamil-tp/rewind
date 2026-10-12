// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"math"
	"sync"
	"time"
)

// Limiter is an in-memory token-bucket rate limiter for authentication
// attempts (login, setup, password change). Each client key has its own
// bucket, and a global bucket caps the total across all keys. It never locks
// an account: once tokens refill, attempts are allowed again.
//
// State is lost on restart and is not shared between processes. Keys are
// client IP addresses taken from the TCP connection; X-Forwarded-For is not
// trusted. While Rewind is loopback-only, every client shares the loopback
// key, so the per-key limit acts as a global limit.
type Limiter struct {
	mu      sync.Mutex
	keys    map[string]*bucket
	global  bucket
	perKey  rate
	all     rate
	maxKeys int
	now     func() time.Time
}

type rate struct {
	burst    float64
	interval time.Duration // time to refill one token
}

type bucket struct {
	tokens float64
	last   time.Time
}

// Default limits: each client may make 5 attempts in a burst, then one every
// 12 seconds (5 per minute); all clients together may make 20 in a burst, then
// one every 3 seconds.
var (
	defaultPerClient = rate{burst: 5, interval: 12 * time.Second}
	defaultGlobal    = rate{burst: 20, interval: 3 * time.Second}
)

const defaultMaxKeys = 4096

// NewLimiter returns a Limiter with the default limits.
func NewLimiter() *Limiter {
	return newLimiter(defaultPerClient, defaultGlobal, defaultMaxKeys, time.Now)
}

// NewLimiterWithLimits returns a Limiter allowing burst attempts per client
// (refilling one every interval) and globalBurst attempts in total
// (refilling one every globalInterval).
func NewLimiterWithLimits(burst int, interval time.Duration, globalBurst int, globalInterval time.Duration) *Limiter {
	return newLimiter(rate{float64(burst), interval}, rate{float64(globalBurst), globalInterval}, defaultMaxKeys, time.Now)
}

func newLimiter(perKey, all rate, maxKeys int, now func() time.Time) *Limiter {
	return &Limiter{
		keys: map[string]*bucket{}, perKey: perKey, all: all, maxKeys: maxKeys, now: now,
		global: bucket{tokens: all.burst, last: now()},
	}
}

// Allow consumes one attempt for key. When the attempt is not allowed it
// returns false and how long to wait before retrying.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	l.global.refill(l.all, now)
	b, ok := l.keys[key]
	if !ok {
		if len(l.keys) >= l.maxKeys {
			l.evictFull(now)
		}
		b = &bucket{tokens: l.perKey.burst, last: now}
		if len(l.keys) < l.maxKeys {
			l.keys[key] = b
		}
		// If the table is still full, the new key gets a fresh, untracked
		// bucket; the global bucket still bounds the total attempt rate.
	}
	b.refill(l.perKey, now)

	if b.tokens < 1 {
		return false, b.wait(l.perKey)
	}
	if l.global.tokens < 1 {
		return false, l.global.wait(l.all)
	}
	b.tokens--
	l.global.tokens--
	return true, 0
}

// evictFull drops buckets that have refilled completely; they carry no state.
func (l *Limiter) evictFull(now time.Time) {
	for k, b := range l.keys {
		b.refill(l.perKey, now)
		if b.tokens >= l.perKey.burst {
			delete(l.keys, k)
		}
	}
}

func (b *bucket) refill(r rate, now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(r.burst, b.tokens+float64(elapsed)/float64(r.interval))
		b.last = now
	}
}

func (b *bucket) wait(r rate) time.Duration {
	missing := 1 - b.tokens
	return time.Duration(math.Ceil(missing * float64(r.interval)))
}
