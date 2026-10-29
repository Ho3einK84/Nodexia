package nodes

import (
	"testing"
)

func TestInstallJobSnapshotDoesNotPanicWhenStreamNil(t *testing.T) {
	// A job with stream == nil must not panic on snapshot()
	job := &installJob{
		id:        "test-job-nil-stream",
		serverID:  1,
		nodeType:  "pasarguard-node",
		nodeName:  "test-pg",
		status:    installStatusRunning,
		stream:    nil, // explicitly nil
	}

	snap := job.snapshot()
	if snap.ID != "test-job-nil-stream" {
		t.Fatalf("snap.ID = %q, want test-job-nil-stream", snap.ID)
	}
	if snap.Output != "" {
		t.Fatalf("snap.Output = %q, want empty", snap.Output)
	}
}

func TestInstallStoreCreateInitializesStream(t *testing.T) {
	store := newInstallStore()
	job := store.create(1, "pasarguard-node", "test-pg", InstallPlan{})
	if job == nil {
		t.Fatalf("store.create returned nil")
	}
	if job.stream == nil {
		t.Fatalf("store.create must initialize job.stream")
	}

	// Calling snapshot immediately must not panic and must return valid state
	snap := job.snapshot()
	if snap.Status != installStatusRunning {
		t.Fatalf("snap.Status = %q, want running", snap.Status)
	}
}

func TestInstallJobSnapshotIncludesPendingStream(t *testing.T) {
	store := newInstallStore()
	job := store.create(1, "pasarguard-node", "test-pg", InstallPlan{})

	// Append partial line (pending, no newline)
	job.appendStream("partial line...")
	snap := job.snapshot()
	if snap.Output != "partial line..." {
		t.Fatalf("snap.Output = %q, want 'partial line...'", snap.Output)
	}

	// Flush and verify
	job.flushPending()
	snap2 := job.snapshot()
	if snap2.Output != "partial line..." {
		t.Fatalf("snap2.Output = %q, want 'partial line...'", snap2.Output)
	}
}
