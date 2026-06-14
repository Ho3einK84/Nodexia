package livemetrics

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

// oneFrameStream emits a single metrics frame (cpu=<value>) then stays open
// until ctx is cancelled, emulating a live collection loop. It counts how many
// times it was invoked so tests can prove the connection is shared.
func oneFrameStream(cpu string, calls *int64) StreamFunc {
	return func(ctx context.Context, _ sshclient.ConnectionRequest, onLine func(string)) error {
		atomic.AddInt64(calls, 1)
		onLine("cpu=" + cpu)
		onLine("=ENDFRAME=")
		<-ctx.Done()
		return ctx.Err()
	}
}

func recvUpdate(t *testing.T, sub *Subscription) Update {
	t.Helper()
	select {
	case u := <-sub.C:
		return u
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an update")
		return Update{}
	}
}

func TestHubFanOutSharesOneStream(t *testing.T) {
	var calls int64
	hub := NewWithStream(oneFrameStream("42.00", &calls))

	subA := hub.Subscribe(7, sshclient.ConnectionRequest{Host: "h"})
	defer subA.Close()
	subB := hub.Subscribe(7, sshclient.ConnectionRequest{Host: "h"})
	defer subB.Close()

	// Both clients watching server 7 must see the frame...
	for _, sub := range []*Subscription{subA, subB} {
		u := recvUpdate(t, sub)
		if u.Metrics == nil || u.Metrics.CPUPercent != 42 {
			t.Fatalf("update = %+v, want CPU 42", u)
		}
	}
	// ...from a SINGLE shared collection stream / broker.
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("stream invoked %d times, want 1 (shared per server)", got)
	}
	if got := hub.activeBrokers(); got != 1 {
		t.Errorf("activeBrokers = %d, want 1", got)
	}
}

func TestHubLatestCachedForLateSubscriber(t *testing.T) {
	var calls int64
	hub := NewWithStream(oneFrameStream("55.00", &calls))

	first := hub.Subscribe(1, sshclient.ConnectionRequest{})
	defer first.Close()
	recvUpdate(t, first) // drain the live frame so latest is cached

	// A client that connects later must immediately get the cached frame
	// without waiting for the next interval, and not start a second stream.
	late := hub.Subscribe(1, sshclient.ConnectionRequest{})
	defer late.Close()
	u := recvUpdate(t, late)
	if u.Metrics == nil || u.Metrics.CPUPercent != 55 {
		t.Fatalf("late subscriber update = %+v, want cached CPU 55", u)
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("stream invoked %d times, want 1", got)
	}
}

func TestHubStopsBrokerWhenIdle(t *testing.T) {
	hub := NewWithStream(oneFrameStream("10.00", new(int64)))
	hub.idleGrace = 20 * time.Millisecond

	sub := hub.Subscribe(3, sshclient.ConnectionRequest{})
	if got := hub.activeBrokers(); got != 1 {
		t.Fatalf("activeBrokers = %d, want 1 after subscribe", got)
	}
	sub.Close()

	deadline := time.Now().Add(2 * time.Second)
	for hub.activeBrokers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("broker was not stopped after the idle grace period")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHubSessionCap(t *testing.T) {
	hub := NewWithStream(oneFrameStream("0", new(int64)))
	if !hub.TryAcquire("admin", 2) {
		t.Fatal("first acquire should succeed")
	}
	if !hub.TryAcquire("admin", 2) {
		t.Fatal("second acquire should succeed")
	}
	if hub.TryAcquire("admin", 2) {
		t.Fatal("third acquire should be rejected by the cap")
	}
	hub.Release("admin")
	if !hub.TryAcquire("admin", 2) {
		t.Fatal("acquire should succeed again after a release")
	}
}
