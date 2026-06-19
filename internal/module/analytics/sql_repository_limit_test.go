package analytics_test

import (
	"context"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

// TestTrafficLimitCRUD exercises the per-server monthly download-limit storage:
// unset by default, set, overwrite (upsert), and clear.
func TestTrafficLimitCRUD(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	serverRepo := servers.NewSQLRepository(runtime.SQL)
	srv, err := serverRepo.Create(ctx, servers.Server{
		Name:               "limit-host",
		Host:               "203.0.113.20",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	repo := analytics.NewSQLRepository(runtime.SQL)

	// Unset by default.
	if _, ok, err := repo.GetTrafficLimit(ctx, srv.ID); err != nil || ok {
		t.Fatalf("GetTrafficLimit() initial = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Set.
	const first = int64(500) * 1024 * 1024 * 1024
	if err := repo.SetTrafficLimit(ctx, srv.ID, first); err != nil {
		t.Fatalf("SetTrafficLimit() error = %v", err)
	}
	got, ok, err := repo.GetTrafficLimit(ctx, srv.ID)
	if err != nil || !ok || got != first {
		t.Fatalf("GetTrafficLimit() after set = (%d, %v, %v), want (%d, true, nil)", got, ok, err, first)
	}

	// Overwrite (upsert path must update, not duplicate).
	const second = int64(2) * 1024 * 1024 * 1024 * 1024
	if err := repo.SetTrafficLimit(ctx, srv.ID, second); err != nil {
		t.Fatalf("SetTrafficLimit() overwrite error = %v", err)
	}
	got, ok, err = repo.GetTrafficLimit(ctx, srv.ID)
	if err != nil || !ok || got != second {
		t.Fatalf("GetTrafficLimit() after overwrite = (%d, %v, %v), want (%d, true, nil)", got, ok, err, second)
	}

	// Clear.
	if err := repo.DeleteTrafficLimit(ctx, srv.ID); err != nil {
		t.Fatalf("DeleteTrafficLimit() error = %v", err)
	}
	if _, ok, err := repo.GetTrafficLimit(ctx, srv.ID); err != nil || ok {
		t.Fatalf("GetTrafficLimit() after clear = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Clearing a non-existent limit again is a no-op.
	if err := repo.DeleteTrafficLimit(ctx, srv.ID); err != nil {
		t.Fatalf("DeleteTrafficLimit() repeat error = %v", err)
	}
}
