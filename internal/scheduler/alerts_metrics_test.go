package scheduler

import (
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
)

func TestCurrentMonthTotalGiB(t *testing.T) {
	currentMonth := time.Now().UTC().Format("2006-01")

	traffic := monitoring.TrafficSnapshot{
		MonthlyRows: []monitoring.TrafficRow{
			{Label: "2000-01", TotalBytes: 5 * bytesPerGiB},
			{Label: currentMonth, TotalBytes: 3 * bytesPerGiB},
		},
	}

	if got := currentMonthTotalGiB(traffic); got != 3 {
		t.Fatalf("currentMonthTotalGiB() = %v, want 3", got)
	}
}

func TestCurrentMonthTotalGiBMissingRow(t *testing.T) {
	traffic := monitoring.TrafficSnapshot{
		MonthlyRows: []monitoring.TrafficRow{
			{Label: "1999-12", TotalBytes: 9 * bytesPerGiB},
		},
	}

	if got := currentMonthTotalGiB(traffic); got != 0 {
		t.Fatalf("currentMonthTotalGiB() = %v, want 0 when current month is absent", got)
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
