// Package ratelimit provides small, dependency-free rate limiting helpers.
package ratelimit

import (
	"sync"
	"time"
)

// LoginThrottle is an in-memory, per-key failed-attempt limiter used to slow
// down brute-force guessing against the single administrator login. It is safe
// for concurrent use.
//
// The first MaxFailures failures for a key are free. Each failure at or beyond
// that threshold locks the key for a duration that grows exponentially from
// BaseLockout up to MaxLockout, so repeated guessing becomes increasingly
// expensive. A successful login (Reset) clears the key immediately.
type LoginThrottle struct {
	maxFailures int
	baseLockout time.Duration
	maxLockout  time.Duration
	now         func() time.Time

	mu      sync.Mutex
	records map[string]*attemptRecord
}

type attemptRecord struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
}

// NewLoginThrottle builds a throttle. Non-positive arguments fall back to safe
// defaults (5 failures, 30s base lockout, 15m max lockout).
func NewLoginThrottle(maxFailures int, baseLockout, maxLockout time.Duration) *LoginThrottle {
	if maxFailures <= 0 {
		maxFailures = 5
	}
	if baseLockout <= 0 {
		baseLockout = 30 * time.Second
	}
	if maxLockout < baseLockout {
		maxLockout = 15 * time.Minute
	}
	if maxLockout < baseLockout {
		maxLockout = baseLockout
	}

	return &LoginThrottle{
		maxFailures: maxFailures,
		baseLockout: baseLockout,
		maxLockout:  maxLockout,
		now:         time.Now,
		records:     make(map[string]*attemptRecord),
	}
}

// Allowed reports whether key may attempt a login now. When locked it returns
// false and the remaining duration the caller should wait.
func (t *LoginThrottle) Allowed(key string) (bool, time.Duration) {
	if t == nil || key == "" {
		return true, 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneLocked(now)

	record, ok := t.records[key]
	if !ok {
		return true, 0
	}
	record.lastSeen = now

	if now.Before(record.lockedUntil) {
		return false, record.lockedUntil.Sub(now)
	}
	return true, 0
}

// RecordFailure registers a failed attempt for key and, once the threshold is
// reached, (re)arms an exponentially growing lockout window. It returns the
// retry-after duration when the key is now locked, or zero otherwise.
func (t *LoginThrottle) RecordFailure(key string) time.Duration {
	if t == nil || key == "" {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.pruneLocked(now)

	record, ok := t.records[key]
	if !ok {
		record = &attemptRecord{}
		t.records[key] = record
	}
	record.failures++
	record.lastSeen = now

	if record.failures < t.maxFailures {
		return 0
	}

	lockout := t.lockoutFor(record.failures)
	record.lockedUntil = now.Add(lockout)
	return lockout
}

// Reset clears any tracked state for key, e.g. after a successful login.
func (t *LoginThrottle) Reset(key string) {
	if t == nil || key == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.records, key)
}

// lockoutFor returns baseLockout doubled once per failure beyond the threshold,
// capped at maxLockout. The shift is bounded so it cannot overflow.
func (t *LoginThrottle) lockoutFor(failures int) time.Duration {
	extra := failures - t.maxFailures
	if extra < 0 {
		extra = 0
	}
	if extra > 20 {
		extra = 20
	}

	lockout := t.baseLockout << uint(extra)
	if lockout <= 0 || lockout > t.maxLockout {
		return t.maxLockout
	}
	return lockout
}

// pruneLocked drops records that are no longer locked and have been idle for a
// while, keeping the map from growing without bound. Callers must hold t.mu.
func (t *LoginThrottle) pruneLocked(now time.Time) {
	idleTTL := t.maxLockout
	if idleTTL < time.Hour {
		idleTTL = time.Hour
	}

	for key, record := range t.records {
		if now.Before(record.lockedUntil) {
			continue
		}
		if now.Sub(record.lastSeen) > idleTTL {
			delete(t.records, key)
		}
	}
}
