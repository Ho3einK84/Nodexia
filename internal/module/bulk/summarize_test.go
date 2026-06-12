package bulk

import (
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/view"
)

func TestSummarizeCounts(t *testing.T) {
	results := []view.BulkServerResultView{
		{Status: "ok"},
		{Status: "ok"},
		{Status: "failed"},
		{Status: "skipped"},
		{Status: ""}, // unknown status counts as failed
	}

	got := summarize("reboot", results)

	if !got.Available {
		t.Error("Available = false, want true")
	}
	if got.Action != "reboot" {
		t.Errorf("Action = %q, want reboot", got.Action)
	}
	if got.Total != 5 {
		t.Errorf("Total = %d, want 5", got.Total)
	}
	if got.OKCount != 2 {
		t.Errorf("OKCount = %d, want 2", got.OKCount)
	}
	if got.FailedCount != 2 {
		t.Errorf("FailedCount = %d, want 2", got.FailedCount)
	}
	if got.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", got.SkippedCount)
	}
}
