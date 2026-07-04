package commandstream

import (
	"testing"
	"time"
)

// TestPruneKeepsQuietRunningSession is the regression test for running sessions
// being pruned by the idle TTL: a long command that emits no output for longer
// than the store TTL must remain readable so its completion still lands.
func TestPruneKeepsQuietRunningSession(t *testing.T) {
	store := New(time.Minute)
	created := store.Create(1, "apt-get -y upgrade")

	// Backdate the session far past the TTL but within the running hard cap.
	stale := time.Now().UTC().Add(-2 * time.Hour)
	store.mu.Lock()
	snapshot := store.sessions[created.ID]
	snapshot.StartedAt = stale
	snapshot.UpdatedAt = stale
	store.sessions[created.ID] = snapshot
	store.mu.Unlock()

	if _, ok := store.Get(created.ID); !ok {
		t.Fatal("running session was pruned by the idle TTL; it must survive until it finishes")
	}

	// Completion must still be recordable and visible.
	exit := 0
	store.Complete(created.ID, &exit, time.Now().UTC(), 42)
	got, ok := store.Get(created.ID)
	if !ok {
		t.Fatal("completed session disappeared")
	}
	if got.Status != StatusCompleted || got.HistoryID != 42 {
		t.Fatalf("unexpected snapshot after completion: status=%q history=%d", got.Status, got.HistoryID)
	}
}

// TestPruneDropsExpiredSessions keeps the existing retention behaviour: finished
// sessions expire on the TTL, and running ones are dropped past the hard cap.
func TestPruneDropsExpiredSessions(t *testing.T) {
	store := New(time.Minute)

	finished := store.Create(1, "echo done")
	exit := 0
	store.Complete(finished.ID, &exit, time.Now().UTC(), 1)

	zombie := store.Create(1, "sleep forever")

	store.mu.Lock()
	f := store.sessions[finished.ID]
	f.UpdatedAt = time.Now().UTC().Add(-2 * time.Minute)
	store.sessions[finished.ID] = f
	z := store.sessions[zombie.ID]
	z.StartedAt = time.Now().UTC().Add(-25 * time.Hour)
	z.UpdatedAt = z.StartedAt
	store.sessions[zombie.ID] = z
	store.mu.Unlock()

	if _, ok := store.Get(finished.ID); ok {
		t.Fatal("finished session older than the TTL must be pruned")
	}
	if _, ok := store.Get(zombie.ID); ok {
		t.Fatal("running session older than the hard cap must be pruned")
	}
}
