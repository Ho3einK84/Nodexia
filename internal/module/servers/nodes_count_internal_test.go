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
}
