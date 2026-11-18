package nodes

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestStreamProcessorExtractsStagesWithoutTrailingNewlineBugOrPanic(t *testing.T) {
	store := newInstallStore()
	job := store.create(1, "rebecca-node", "r1", InstallPlan{})

	// Split marker across two chunks (matching stageMarker's "\n[NODEXIA:stage:...]\n" shape)
	// and include literal "[NODEXIA]\n" (regression guard for out-of-bounds panic when len == 9).
	job.appendStream("line one\n\n[NODEX")
	job.appendStream("IA:stage:downloading]\n\n[NODEXIA]\nline two\n")
	job.flushPending()

	snap := job.snapshot()
	if snap.Stage != stageDownloading {
		t.Fatalf("Stage = %q, want %q", snap.Stage, stageDownloading)
	}
	if strings.Contains(snap.Output, "[NODEXIA:stage:") {
		t.Fatalf("stage marker leaked into output:\n%s", snap.Output)
	}
	if !strings.Contains(snap.Output, "[NODEXIA]\n") || !strings.Contains(snap.Output, "line one\n") || !strings.Contains(snap.Output, "line two\n") {
		t.Fatalf("unexpected cleaned output: %q", snap.Output)
	}
}

func TestInstallOutputTruncationPreservesValidUTF8(t *testing.T) {
	prefix := strings.Repeat("a", maxInstallOutputBytes-2)
	chunk := "سلام" // 8 UTF-8 bytes; only 2 bytes remain before maxInstallOutputBytes
	store := newInstallStore()
	job := store.create(1, "pasarguard-node", "p1", InstallPlan{})

	job.appendOutput(prefix)
	job.appendOutput(chunk)

	snap := job.snapshot()
	if !utf8.ValidString(snap.Output) {
		t.Fatal("truncated job output contains invalid UTF-8 split across rune boundary")
	}
	if !strings.Contains(snap.Output, "[output truncated at 1 MB]") {
		t.Fatal("expected truncation notice in output")
	}
}

func TestInstallJobCancelTriggersContextCancel(t *testing.T) {
	store := newInstallStore()
	job := store.create(1, "pasarguard-node", "p1", InstallPlan{})

	ctx, cancel := context.WithCancel(context.Background())
	job.setCancel(cancel)

	if !job.Cancel() {
		t.Fatal("Cancel() = false, want true while running")
	}
	if ctx.Err() == nil {
		t.Fatal("expected cancel func to cancel job context")
	}
	job.fail("canceled")
	snap := job.snapshot()
	if snap.Status != installStatusCanceled {
		t.Fatalf("Status = %q, want %q", snap.Status, installStatusCanceled)
	}
	if job.Cancel() {
		t.Fatal("Cancel() must return false after job already finished")
	}
}
