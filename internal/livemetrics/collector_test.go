package livemetrics

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFrameParser(t *testing.T) {
	lines := []string{
		"cpu=42.50",
		"core=cpu0 50.00",
		"core=cpu1 35.00",
		"mem=16384000 8192000 2048000 8192000 50.00",
		"swap=25.00",
		"disk=/ 60 51200000 102400000",
		"disk=/data 20 104857600 524288000",
		"load=0.50 0.40 0.30",
		"uptime=123456",
		"net=1000000 2000000",
		"ts=1700000000",
		"=ENDFRAME=",
	}

	parser := &frameParser{}
	var got *Metrics
	for _, line := range lines {
		if m, ok := parser.line(line); ok {
			got = m
		}
	}
	if got == nil {
		t.Fatal("parser did not produce a Metrics on the frame delimiter")
	}

	if got.CPUPercent != 42.5 {
		t.Errorf("CPUPercent = %v, want 42.5", got.CPUPercent)
	}
	if len(got.PerCore) != 2 || got.PerCore[0] != 50 || got.PerCore[1] != 35 {
		t.Errorf("PerCore = %v, want [50 35]", got.PerCore)
	}
	if got.MemTotalKB != 16384000 || got.MemUsedKB != 8192000 || got.MemPercent != 50 {
		t.Errorf("mem = %+v, want total 16384000 used 8192000 pct 50", got)
	}
	if got.SwapPercent != 25 {
		t.Errorf("SwapPercent = %v, want 25", got.SwapPercent)
	}
	if len(got.Disks) != 2 || got.Disks[0].Mount != "/" || got.Disks[1].Mount != "/data" {
		t.Errorf("Disks = %+v, want / and /data", got.Disks)
	}
	if got.Disks[0].Percent != 60 || got.Disks[0].TotalKB != 102400000 {
		t.Errorf("Disks[0] = %+v", got.Disks[0])
	}
	if got.Load1 != 0.5 || got.Load5 != 0.4 || got.Load15 != 0.3 {
		t.Errorf("load = %v/%v/%v", got.Load1, got.Load5, got.Load15)
	}
	if got.UptimeSeconds != 123456 {
		t.Errorf("UptimeSeconds = %v, want 123456", got.UptimeSeconds)
	}
	if got.NetRxBytes != 1000000 || got.NetTxBytes != 2000000 {
		t.Errorf("net = %v/%v", got.NetRxBytes, got.NetTxBytes)
	}
	if !got.CollectedAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("CollectedAt = %v, want unix 1700000000", got.CollectedAt)
	}
}

func TestFrameParserIgnoresPartialAndJunk(t *testing.T) {
	parser := &frameParser{}
	for _, line := range []string{"", "garbage-without-equals", "cpu=10.00"} {
		if _, ok := parser.line(line); ok {
			t.Fatal("no frame should complete before the delimiter")
		}
	}
	m, ok := parser.line("=ENDFRAME=")
	if !ok || m == nil {
		t.Fatal("delimiter should flush the accumulated frame")
	}
	if m.CPUPercent != 10 {
		t.Errorf("CPUPercent = %v, want 10", m.CPUPercent)
	}
	// A bare delimiter with no preceding keys must not emit an empty frame.
	if _, ok := parser.line("=ENDFRAME="); ok {
		t.Error("delimiter with no data must not flush a frame")
	}
}

// TestCollectCommandShellSyntax syntax-checks the generated remote loop with
// `sh -n` (mirrors the nodes module guard) and asserts it stays free of single
// quotes, which would break the sh -c '...' wrapper.
func TestCollectCommandShellSyntax(t *testing.T) {
	command := collectCommand(DefaultInterval)
	inner := strings.TrimSuffix(strings.TrimPrefix(command, "sh -c '"), "'")
	if strings.Contains(inner, "'") {
		t.Fatalf("generated command contains a single quote (breaks sh -c wrapping):\n%s", inner)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available on PATH")
	}
	out, err := exec.Command("sh", "-n", "-c", inner).CombinedOutput()
	if err != nil {
		t.Fatalf("sh -n failed: %v\n%s\ncommand:\n%s", err, out, inner)
	}
}
