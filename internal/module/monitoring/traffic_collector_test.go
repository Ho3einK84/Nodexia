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

// ── Interface selection ───────────────────────────────────────────────────────

func monthIface(name string, rx, tx int64) vnstatInterface {
	return vnstatInterface{
		Name: name,
		Traffic: vnstatTraffic{
			Months: []vnstatMonth{{Date: vnstatMonthDate{Year: 2026, Month: 6}, RX: rx, TX: tx}},
		},
	}
}

// dockerHostInterfaces mirrors a real VPS where the active NIC (eth0) is dwarfed
// in count by disabled docker bridges/veth pairs — the layout that previously
// caused the panel to auto-detect "br-…" instead of "eth0".
func dockerHostInterfaces() []vnstatInterface {
	return []vnstatInterface{
		monthIface("br-5ae0cfaebf1f", 59_000_000, 303_000_000),
		monthIface("docker0", 224, 660),
		monthIface("eth0", 233_000_000_000, 234_000_000_000),
		monthIface("veth542fc41", 38_000_000, 126_000_000),
		monthIface("vethf65f6e6", 39_000_000, 9_000_000),
	}
}

func ifaceName(i *vnstatInterface) string {
	if i == nil {
		return "<nil>"
	}
	return i.Name
}

func TestSelectTrafficInterfacePrefersPhysicalNIC(t *testing.T) {
	got, _ := selectTrafficInterface(dockerHostInterfaces(), "")
	if ifaceName(got) != "eth0" {
		t.Fatalf("auto-select = %q, want eth0", ifaceName(got))
	}
}

func TestSelectTrafficInterfaceOverridesStaleVirtualPreference(t *testing.T) {
	// A stale auto-detect (or empty user config falling back to the last stored
	// interface) must not pin monitoring to a docker bridge when eth0 is active.
	got, _ := selectTrafficInterface(dockerHostInterfaces(), "br-5ae0cfaebf1f")
	if ifaceName(got) != "eth0" {
		t.Fatalf("stale docker-bridge preference = %q, want eth0", ifaceName(got))
	}
}

func TestSelectTrafficInterfaceHonoursExplicitPhysicalPreference(t *testing.T) {
	items := append(dockerHostInterfaces(), monthIface("eth1", 1_000_000, 1_000_000))
	got, _ := selectTrafficInterface(items, "eth1")
	if ifaceName(got) != "eth1" {
		t.Fatalf("explicit eth1 preference = %q, want eth1 (must not be overridden by busier eth0)", ifaceName(got))
	}
}

func TestSelectTrafficInterfaceKeepsVirtualWhenNoPhysical(t *testing.T) {
	items := []vnstatInterface{
		monthIface("docker0", 10, 10),
		monthIface("br-abc", 1000, 1000),
	}
	got, _ := selectTrafficInterface(items, "")
	if ifaceName(got) != "br-abc" {
		t.Fatalf("all-virtual auto-select = %q, want br-abc (busiest)", ifaceName(got))
	}
}

func TestParseVnstatTextHandlesDisabledInterfaces(t *testing.T) {
	const out = `                      rx      /      tx      /     total    /   estimated
 br-5ae0cfaebf1f [disabled]:
       2026-06     56.74 MiB  /  289.71 MiB  /  346.45 MiB  /    1.75 GiB
     yesterday     14.63 MiB  /  128.68 MiB  /  143.31 MiB
         today      2.26 MiB  /   28.44 MiB  /   30.70 MiB  /  147.37 MiB
 docker0:
       2026-06         224 B  /       660 B  /       884 B  /     --
 eth0:
       2026-06    217.28 GiB  /  218.51 GiB  /  435.79 GiB  /    1.93 TiB
     yesterday     83.95 GiB  /   84.44 GiB  /  168.39 GiB
         today     15.77 GiB  /   15.79 GiB  /   31.56 GiB  /   57.89 GiB
 veth542fc41 [disabled]:
       2026-06     36.52 MiB  /  121.05 MiB  /  157.57 MiB  /  814.94 MiB
 vethf65f6e6 [disabled]:
       2026-06     37.24 MiB  /    8.75 MiB  /   45.99 MiB  /    6.98 GiB
     2026-06-18    37.24 MiB  /    8.75 MiB  /   45.99 MiB  /   48.80 MiB`

	interfaces := parseVnstatText(out)
	byName := map[string]vnstatInterface{}
	for _, iface := range interfaces {
		byName[iface.Name] = iface
	}
	for _, want := range []string{"br-5ae0cfaebf1f", "docker0", "eth0", "veth542fc41", "vethf65f6e6"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("parseVnstatText did not return disabled-aware interface %q", want)
		}
	}

	// eth0 must keep only its own rows — the disabled veth rows that follow it in
	// the output must not be misattributed to it.
	eth0 := byName["eth0"]
	if len(eth0.Traffic.Months) != 1 {
		t.Fatalf("eth0 month rows = %d, want 1 (disabled-interface rows leaked into eth0)", len(eth0.Traffic.Months))
	}

	selected, _ := selectTrafficInterface(interfaces, "")
	if ifaceName(selected) != "eth0" {
		t.Fatalf("selectTrafficInterface = %q, want eth0", ifaceName(selected))
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
