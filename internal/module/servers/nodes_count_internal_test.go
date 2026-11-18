package servers

import (
	"context"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

// TestTotalNodeCount checks that the servers-page node tally sums only each
// server's latest discovery sweep, excludes the "none" sentinel, and reads 0
// before any discovery has run.
func TestTotalNodeCount(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	deps := module.Dependencies{Config: testutil.TestConfig(t), Database: runtime}
	repo := NewSQLRepository(runtime.SQL)
	ctx := context.Background()

	if got := totalNodeCount(ctx, deps); got != 0 {
		t.Fatalf("totalNodeCount with no snapshots = %d, want 0", got)
	}

	s1, err := repo.Create(ctx, Server{Name: "s1", Host: "10.0.0.1", Port: 22, AuthMode: AuthModePassword, Username: "root"})
	if err != nil {
		t.Fatalf("create s1: %v", err)
	}
	s2, err := repo.Create(ctx, Server{Name: "s2", Host: "10.0.0.2", Port: 22, AuthMode: AuthModePassword, Username: "root"})
	if err != nil {
		t.Fatalf("create s2: %v", err)
	}

	ins := func(serverID int64, nodeType, createdAt string) {
		t.Helper()
		if _, err := runtime.SQL.ExecContext(ctx,
			`INSERT INTO node_snapshots (server_id, node_type, created_at) VALUES (?, ?, ?)`,
			serverID, nodeType, createdAt); err != nil {
			t.Fatalf("insert snapshot: %v", err)
		}
	}

	const older = "2026-06-01 10:00:00"
	const newer = "2026-06-01 12:00:00"

	// s1: an older sweep with 3 instances, then a newer sweep with 2. Only the
	// newer batch (higher ids) must count.
	ins(s1.ID, "pasarguard-node", older)
	ins(s1.ID, "pasarguard-node", older)
	ins(s1.ID, "rebecca-node", older)
	ins(s1.ID, "pasarguard-node", newer)
	ins(s1.ID, "rebecca-node", newer)
	// s2: discovery ran but found nothing — a single "none" sentinel row.
	ins(s2.ID, "none", older)

	if got := totalNodeCount(ctx, deps); got != 2 {
		t.Errorf("totalNodeCount = %d, want 2 (s1 latest sweep only, s2 none excluded)", got)
	}

	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO node_snapshots (server_id, node_type, health_status, created_at) VALUES (?, ?, ?, ?)`,
		s1.ID, "pasarguard-node", "running", "2026-06-01 13:00:00"); err != nil {
		t.Fatalf("insert running snapshot: %v", err)
	}
	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO node_snapshots (server_id, node_type, health_status, created_at) VALUES (?, ?, ?, ?)`,
		s1.ID, "rebecca-node", "stopped", "2026-06-01 13:00:00"); err != nil {
		t.Fatalf("insert stopped snapshot: %v", err)
	}
	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO system_snapshots (server_id, cpu_usage, ram_usage, disk_usage, created_at) VALUES (?, ?, ?, ?, ?)`,
		s1.ID, 24.6, 62.2, 88.9, "2026-06-01 13:05:00"); err != nil {
		t.Fatalf("insert system snapshot: %v", err)
	}

	counts := serverNodeCountsMap(ctx, deps)
	if counts[s1.ID].total != 2 || counts[s1.ID].running != 1 || counts[s1.ID].stopped != 1 {
		t.Errorf("serverNodeCountsMap[s1] = %+v, want total=2 running=1 stopped=1", counts[s1.ID])
	}
	if _, ok := counts[s2.ID]; ok {
		t.Errorf("serverNodeCountsMap[s2] should be absent for none sentinel, got %+v", counts[s2.ID])
	}

	tel := serverTelemetryMap(ctx, deps)
	gotTel, ok := tel[s1.ID]
	if !ok {
		t.Fatal("serverTelemetryMap missing s1")
	}
	if clampPercent(gotTel.cpu) != 25 || clampPercent(gotTel.ram) != 62 || clampPercent(gotTel.disk) != 89 {
		t.Errorf("unexpected telemetry rounded percentages: %+v", gotTel)
	}
	if usageLevel(clampPercent(gotTel.cpu)) != "ok" || usageLevel(clampPercent(gotTel.ram)) != "warn" || usageLevel(clampPercent(gotTel.disk)) != "crit" {
		t.Errorf("unexpected usage levels for cpu/ram/disk: %+v", gotTel)
	}
}
