package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

func TestJobRunsRecordAndList(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	srv, err := servers.NewSQLRepository(runtime.SQL).Create(ctx, servers.Server{
		Name: "runs-host", Host: "203.0.113.40", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create server: %v", err)
	}

	repo := newJobRunsRepository(runtime.SQL)
	now := time.Now().UTC().Truncate(time.Second)

	if err := repo.Record(ctx, JobRun{
		ServerID: srv.ID, JobType: JobMonitoring,
		StartedAt: now.Add(-30 * time.Second), FinishedAt: now,
		Duration: 30 * time.Second, Success: true, Message: "Stored resource and vnStat snapshots.",
	}); err != nil {
		t.Fatalf("Record success: %v", err)
	}
	if err := repo.Record(ctx, JobRun{
		ServerID: srv.ID, JobType: JobNodes,
		StartedAt: now, FinishedAt: now.Add(2 * time.Second),
		Duration: 2 * time.Second, Success: false, Error: "sshclient: handshake failed",
	}); err != nil {
		t.Fatalf("Record failure: %v", err)
	}

	runs, err := repo.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("ListRecent returned %d runs, want 2", len(runs))
	}
	// Newest first.
	if runs[0].JobType != JobNodes || runs[0].Success || runs[0].Error == "" {
		t.Fatalf("newest run mismatch: %+v", runs[0])
	}
	if runs[1].JobType != JobMonitoring || !runs[1].Success || runs[1].ServerName != "runs-host" {
		t.Fatalf("oldest run mismatch: %+v", runs[1])
	}
	if runs[1].Duration != 30*time.Second {
		t.Fatalf("Duration = %s, want 30s", runs[1].Duration)
	}
	if runs[1].StartedAt.IsZero() {
		t.Fatal("StartedAt did not round-trip")
	}

	// A nil repository (no database) must be a safe no-op everywhere.
	var nilRepo *jobRunsRepository
	if err := nilRepo.Record(ctx, JobRun{}); err != nil {
		t.Fatalf("nil Record: %v", err)
	}
	if rows, err := nilRepo.ListRecent(ctx, 5); err != nil || rows != nil {
		t.Fatalf("nil ListRecent = (%v, %v), want (nil, nil)", rows, err)
	}
}
