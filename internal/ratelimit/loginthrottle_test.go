package ratelimit

import (
	"testing"
	"time"
)

func newClockedThrottle(maxFailures int, base, max time.Duration, clock *time.Time) *LoginThrottle {
	t := NewLoginThrottle(maxFailures, base, max)
	t.now = func() time.Time { return *clock }
	return t
}

func TestThrottleAllowsBelowThreshold(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newClockedThrottle(3, time.Minute, 10*time.Minute, &now)

	for i := 0; i < 2; i++ {
		if locked := throttle.RecordFailure("1.2.3.4"); locked != 0 {
			t.Fatalf("failure %d unexpectedly locked for %s", i+1, locked)
		}
		if allowed, _ := throttle.Allowed("1.2.3.4"); !allowed {
			t.Fatalf("key should still be allowed after %d failures", i+1)
		}
	}
}

func TestThrottleLocksAtThreshold(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newClockedThrottle(3, time.Minute, 10*time.Minute, &now)

	throttle.RecordFailure("1.2.3.4")
	throttle.RecordFailure("1.2.3.4")
	locked := throttle.RecordFailure("1.2.3.4")
	if locked != time.Minute {
		t.Fatalf("third failure lockout = %s, want 1m", locked)
	}

	allowed, retryAfter := throttle.Allowed("1.2.3.4")
	if allowed {
		t.Fatal("key should be locked at threshold")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Fatalf("retryAfter = %s, want (0, 1m]", retryAfter)
	}
}

func TestThrottleUnlocksAfterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newClockedThrottle(1, time.Minute, 10*time.Minute, &now)

	throttle.RecordFailure("1.2.3.4")
	if allowed, _ := throttle.Allowed("1.2.3.4"); allowed {
		t.Fatal("key should be locked immediately at threshold 1")
	}

	now = now.Add(time.Minute + time.Second)
	if allowed, _ := throttle.Allowed("1.2.3.4"); !allowed {
		t.Fatal("key should be allowed after lockout window elapses")
	}
}

func TestThrottleLockoutGrowsExponentially(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newClockedThrottle(1, time.Minute, time.Hour, &now)

	if got := throttle.RecordFailure("1.2.3.4"); got != time.Minute {
		t.Fatalf("first lockout = %s, want 1m", got)
	}
	if got := throttle.RecordFailure("1.2.3.4"); got != 2*time.Minute {
		t.Fatalf("second lockout = %s, want 2m", got)
	}
	if got := throttle.RecordFailure("1.2.3.4"); got != 4*time.Minute {
		t.Fatalf("third lockout = %s, want 4m", got)
	}
}

func TestThrottleLockoutCappedAtMax(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newClockedThrottle(1, time.Minute, 3*time.Minute, &now)

	throttle.RecordFailure("1.2.3.4") // 1m
	throttle.RecordFailure("1.2.3.4") // 2m
	if got := throttle.RecordFailure("1.2.3.4"); got != 3*time.Minute {
		t.Fatalf("capped lockout = %s, want 3m", got)
	}
	if got := throttle.RecordFailure("1.2.3.4"); got != 3*time.Minute {
		t.Fatalf("lockout beyond cap = %s, want 3m", got)
	}
}

func TestThrottleResetClearsState(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newClockedThrottle(2, time.Minute, 10*time.Minute, &now)

	throttle.RecordFailure("1.2.3.4")
	throttle.RecordFailure("1.2.3.4")
	if allowed, _ := throttle.Allowed("1.2.3.4"); allowed {
		t.Fatal("key should be locked before reset")
	}

	throttle.Reset("1.2.3.4")
	if allowed, _ := throttle.Allowed("1.2.3.4"); !allowed {
		t.Fatal("key should be allowed after reset")
	}
}

func TestThrottleKeysAreIndependent(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newClockedThrottle(1, time.Minute, 10*time.Minute, &now)

	throttle.RecordFailure("1.1.1.1")
	if allowed, _ := throttle.Allowed("1.1.1.1"); allowed {
		t.Fatal("1.1.1.1 should be locked")
	}
	if allowed, _ := throttle.Allowed("2.2.2.2"); !allowed {
		t.Fatal("2.2.2.2 must not be affected by 1.1.1.1")
	}
}

func TestThrottleNilAndEmptyKeyAreSafe(t *testing.T) {
	var throttle *LoginThrottle
	if allowed, _ := throttle.Allowed("x"); !allowed {
		t.Fatal("nil throttle must allow")
	}
	if locked := throttle.RecordFailure("x"); locked != 0 {
		t.Fatal("nil throttle must not lock")
	}
	throttle.Reset("x") // must not panic

	real := NewLoginThrottle(1, time.Minute, time.Minute)
	if allowed, _ := real.Allowed(""); !allowed {
		t.Fatal("empty key must always be allowed")
	}
	if locked := real.RecordFailure(""); locked != 0 {
		t.Fatal("empty key must never lock")
	}
}
