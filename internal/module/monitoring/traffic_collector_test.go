package monitoring

import (
	"math"
	"testing"
)

// bandwidthFixture uses hours where RX and TX differ markedly so a regression to
// the old RX+TX behaviour produces a different (and detectably wrong) result.
//
// mbps = bytes * 8 / 3600 / 1e6, i.e. bytes / 450e6.
//   - Hour1: RX 900e6 → 2.0 Mbps download; TX 100e6.
//   - Hour2: RX 450e6 → 1.0 Mbps download; TX 1350e6.
//
// Download-only peak is Hour1 (2.0). If RX+TX were summed, Hour2 would win
// (1800e6 → 4.0 Mbps), so the peak would shift to the wrong hour and value.
func bandwidthFixture() []vnstatHour {
	return []vnstatHour{
		{RX: 900_000_000, TX: 100_000_000},
		{RX: 450_000_000, TX: 1_350_000_000},
	}
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestComputePeakMbpsDownloadOnly(t *testing.T) {
	got := computePeakMbps(bandwidthFixture())
	const want = 2.0 // RX-only peak (Hour1); RX+TX would give 4.0 (Hour2).
	if !approxEqual(got, want) {
		t.Fatalf("computePeakMbps = %v, want %v (download/RX only)", got, want)
	}
}

func TestComputeAvgMbpsDownloadOnly(t *testing.T) {
	got := computeAvgMbps(bandwidthFixture())
	const want = 1.5 // (2.0 + 1.0) / 2; RX+TX would give (4.0 + ...) larger.
	if !approxEqual(got, want) {
		t.Fatalf("computeAvgMbps = %v, want %v (download/RX only)", got, want)
	}
}

func TestComputeAvgMbpsEmpty(t *testing.T) {
	if got := computeAvgMbps(nil); got != 0 {
		t.Fatalf("computeAvgMbps(nil) = %v, want 0", got)
	}
}

func TestComputePeakMbpsEmpty(t *testing.T) {
	if got := computePeakMbps(nil); got != 0 {
		t.Fatalf("computePeakMbps(nil) = %v, want 0", got)
	}
}

// TestRetainedDailyRowsSupportsSeasonality guards the daily-history retention:
// weekly seasonality in the analytics forecast needs several samples per weekday,
// so a single week is not enough. tailTrafficRows must keep the configured window
// (35 days / 5 weeks), and that window must cover at least four full weeks.
func TestRetainedDailyRowsSupportsSeasonality(t *testing.T) {
	if retainedDailyRows < 28 {
		t.Fatalf("retainedDailyRows = %d, want >= 28 (4 weeks) to support weekly seasonality", retainedDailyRows)
	}

	rows := make([]TrafficRow, 60) // more than the retention window
	for i := range rows {
		rows[i] = TrafficRow{Label: "2026-01-01", RXBytes: int64(i)}
	}
	got := tailTrafficRows(rows, retainedDailyRows)
	if len(got) != retainedDailyRows {
		t.Fatalf("tailTrafficRows kept %d rows, want %d", len(got), retainedDailyRows)
	}
	// It must keep the most recent rows (tail), not the head.
	if got[len(got)-1].RXBytes != rows[len(rows)-1].RXBytes {
		t.Fatalf("tailTrafficRows did not keep the newest row: got %d, want %d",
			got[len(got)-1].RXBytes, rows[len(rows)-1].RXBytes)
	}
}
