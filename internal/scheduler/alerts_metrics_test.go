package scheduler

import (
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
)

func TestCurrentPeriodTotalGiB(t *testing.T) {
	now := time.Now().UTC()
	currentMonth := now.Format("2006-01")

	traffic := monitoring.TrafficSnapshot{
		MonthlyRows: []monitoring.TrafficRow{
			{Label: "2000-01", TotalBytes: 5 * bytesPerGiB},
			{Label: currentMonth, TotalBytes: 3 * bytesPerGiB},
		},
	}

	if got := currentPeriodTotalGiB(traffic, now, 1); got != 3 {
		t.Fatalf("currentPeriodTotalGiB() = %v, want 3", got)
	}
}

func TestCurrentPeriodTotalGiBMissingRow(t *testing.T) {
	now := time.Now().UTC()
	traffic := monitoring.TrafficSnapshot{
		MonthlyRows: []monitoring.TrafficRow{
			{Label: "1999-12", TotalBytes: 9 * bytesPerGiB},
		},
	}

	if got := currentPeriodTotalGiB(traffic, now, 1); got != 0 {
		t.Fatalf("currentPeriodTotalGiB() = %v, want 0 when current month is absent", got)
	}
}

// TestCurrentPeriodTotalGiBAnchored covers the billing-cycle anchor: only daily
// rows on/after the anchored period start count, and the monthly rows are
// ignored entirely (they describe calendar months, not the anchored period).
func TestCurrentPeriodTotalGiBAnchored(t *testing.T) {
	// Fixed date far from month boundaries: 2026-06-20, reset day 15 → period
	// runs 2026-06-15 .. 2026-07-14.
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	traffic := monitoring.TrafficSnapshot{
		DailyRows: []monitoring.TrafficRow{
			{Label: "2026-06-10", TotalBytes: 7 * bytesPerGiB}, // before the anchor — excluded
			{Label: "2026-06-15", TotalBytes: 2 * bytesPerGiB},
			{Label: "2026-06-19", RXBytes: 1 * bytesPerGiB, TXBytes: 1 * bytesPerGiB}, // total falls back to RX+TX
		},
		MonthlyRows: []monitoring.TrafficRow{
			{Label: "2026-06", TotalBytes: 999 * bytesPerGiB}, // must be ignored when anchored
		},
	}

	if got := currentPeriodTotalGiB(traffic, now, 15); got != 4 {
		t.Fatalf("currentPeriodTotalGiB(anchored) = %v, want 4", got)
	}
}

func TestAnyNodeStopped(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []nodes.Snapshot
		want      bool
	}{
		{"empty", nil, false},
		{"all running", []nodes.Snapshot{{HealthStatus: "running"}, {HealthStatus: "running"}}, false},
		{"one stopped", []nodes.Snapshot{{HealthStatus: "running"}, {HealthStatus: "stopped"}}, true},
		{"stopped case-insensitive padded", []nodes.Snapshot{{HealthStatus: " Stopped "}}, true},
		{"unknown does not count", []nodes.Snapshot{{HealthStatus: "unknown"}}, false},
		{"placeholder does not count", []nodes.Snapshot{{HealthStatus: "not_detected"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := anyNodeStopped(tc.snapshots); got != tc.want {
				t.Fatalf("anyNodeStopped() = %v, want %v", got, tc.want)
			}
		})
	}
}
