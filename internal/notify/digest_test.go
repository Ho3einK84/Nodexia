package notify

import (
	"strings"
	"testing"
)

func TestRenderDigestPopulated(t *testing.T) {
	msg := DigestMessage{
		GeneratedAt:  "2026-06-19 09:00 UTC",
		ServerCount:  2,
		ActiveAlerts: 1,
		Servers: []DigestServer{
			{Name: "edge-1", MonthDownload: "120.50 GiB", MonthTotal: "150.00 GiB", LimitState: "⚠️ Projected to reach limit in 2 day(s) on 2026-06-21 (limit 500.00 GiB)", ActiveAlerts: 1},
			{Name: "edge-2", MonthDownload: "no data yet", MonthTotal: "no data yet", LimitState: "No monthly download limit set"},
		},
	}

	out, err := RenderDigest("", msg)
	if err != nil {
		t.Fatalf("RenderDigest() error = %v", err)
	}
	for _, want := range []string{
		"2026-06-19 09:00 UTC",
		"2 server(s) · 1 active alert(s)",
		"edge-1",
		"120.50 GiB",
		"Projected to reach limit in 2 day(s)",
		"🔔 1 active alert(s)",
		"edge-2",
		"No monthly download limit set",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("digest missing %q:\n%s", want, out)
		}
	}
	// edge-2 has no active alerts, so the per-server alert line must not appear for it.
	if strings.Count(out, "🔔") != 1 {
		t.Fatalf("expected exactly one per-server alert line, got:\n%s", out)
	}
}

func TestRenderDigestEmpty(t *testing.T) {
	out, err := RenderDigest("", DigestMessage{GeneratedAt: "2026-06-19 09:00 UTC"})
	if err != nil {
		t.Fatalf("RenderDigest() error = %v", err)
	}
	if !strings.Contains(out, "No servers registered yet") {
		t.Fatalf("empty digest missing empty-state line:\n%s", out)
	}
	if !strings.Contains(out, "0 server(s) · 0 active alert(s)") {
		t.Fatalf("empty digest missing counts:\n%s", out)
	}
}
