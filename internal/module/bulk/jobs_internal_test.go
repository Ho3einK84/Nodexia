package bulk

import (
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/view"
)

func TestJobSnapshotIsACopy(t *testing.T) {
	j := &job{rows: []view.BulkServerResultView{{ID: 1, Status: statusPending}}}

	rows, finished := j.snapshot()
	if finished {
		t.Fatal("new job reported finished")
	}
	rows[0].Status = statusOK // mutate the copy

	if got, _ := j.snapshot(); got[0].Status != statusPending {
		t.Errorf("snapshot mutation leaked into job: status = %q", got[0].Status)
	}
}

func TestJobStorePrunesExpired(t *testing.T) {
	store := newJobStore()

	fresh := store.create("reboot", nil)
	expired := store.create("reboot", nil)
	expired.finish()
	expired.mu.Lock()
	expired.finishedAt = time.Now().Add(-finishedJobTTL - time.Minute)
	expired.mu.Unlock()

	if _, ok := store.get(expired.id); ok {
		t.Error("expired finished job survived prune")
	}
	if _, ok := store.get(fresh.id); !ok {
		t.Error("fresh job was pruned")
	}
}

// TestJobStoreKeepsLongLiveJobs is the regression test for unfinished jobs
// being pruned staleJobTTL after creation even while workers were still
// running: a long fleet run with recent row activity must stay readable, while
// a truly stalled job (no activity at all) is still dropped.
func TestJobStoreKeepsLongLiveJobs(t *testing.T) {
	store := newJobStore()

	live := store.create("update", make([]view.BulkServerResultView, 1))
	live.mu.Lock()
	live.createdAt = time.Now().Add(-staleJobTTL - time.Hour) // long past creation
	live.mu.Unlock()
	live.setStatus(0, statusRunning) // recent worker activity

	stalled := store.create("update", make([]view.BulkServerResultView, 1))
	stalled.mu.Lock()
	stalled.createdAt = time.Now().Add(-staleJobTTL - time.Hour)
	stalled.mu.Unlock()

	if _, ok := store.get(live.id); !ok {
		t.Error("long-running job with recent activity was pruned mid-run")
	}
	if _, ok := store.get(stalled.id); ok {
		t.Error("stalled job with no activity survived prune")
	}
}

func TestJobSetRowAndStatusBoundsChecked(t *testing.T) {
	j := &job{rows: make([]view.BulkServerResultView, 1)}
	// Out-of-range updates must be ignored, not panic.
	j.setStatus(-1, statusRunning)
	j.setStatus(5, statusRunning)
	j.setRow(7, view.BulkServerResultView{})
	j.setStatus(0, statusRunning)
	if rows, _ := j.snapshot(); rows[0].Status != statusRunning {
		t.Errorf("in-range setStatus did not apply: %q", rows[0].Status)
	}
}
