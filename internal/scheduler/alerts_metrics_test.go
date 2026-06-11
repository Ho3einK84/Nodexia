package scheduler

import (
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
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
