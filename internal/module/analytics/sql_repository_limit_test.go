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

// TestScopedLimitCRUD exercises the global/tag limit storage: unset by default,
// set, overwrite (upsert), list ordering, and clear.
func TestScopedLimitCRUD(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	repo := analytics.NewSQLRepository(runtime.SQL)

	if _, ok, err := repo.GetScopedLimit(ctx, analytics.LimitScopeGlobal, ""); err != nil || ok {
		t.Fatalf("GetScopedLimit() initial = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	const gib = int64(1024) * 1024 * 1024
	if err := repo.SetScopedLimit(ctx, analytics.LimitScopeGlobal, "", 100*gib); err != nil {
		t.Fatalf("SetScopedLimit(global) error = %v", err)
	}
	if err := repo.SetScopedLimit(ctx, analytics.LimitScopeTag, "hetzner", 200*gib); err != nil {
		t.Fatalf("SetScopedLimit(tag) error = %v", err)
	}
	// Overwrite must update, not duplicate.
	if err := repo.SetScopedLimit(ctx, analytics.LimitScopeTag, "hetzner", 250*gib); err != nil {
		t.Fatalf("SetScopedLimit(tag) overwrite error = %v", err)
	}
	got, ok, err := repo.GetScopedLimit(ctx, analytics.LimitScopeTag, "hetzner")
	if err != nil || !ok || got != 250*gib {
		t.Fatalf("GetScopedLimit(tag) = (%d, %v, %v), want (%d, true, nil)", got, ok, err, 250*gib)
	}

	rules, err := repo.ListScopedLimits(ctx)
	if err != nil || len(rules) != 2 {
		t.Fatalf("ListScopedLimits() = (%d rules, %v), want (2, nil)", len(rules), err)
	}
	// Global is listed first.
	if rules[0].Scope != analytics.LimitScopeGlobal {
		t.Fatalf("ListScopedLimits()[0].Scope = %q, want global", rules[0].Scope)
	}

	if err := repo.DeleteScopedLimit(ctx, analytics.LimitScopeTag, "hetzner"); err != nil {
		t.Fatalf("DeleteScopedLimit() error = %v", err)
	}
	if _, ok, _ := repo.GetScopedLimit(ctx, analytics.LimitScopeTag, "hetzner"); ok {
		t.Fatalf("GetScopedLimit(tag) after delete = ok, want absent")
	}
}

// TestResolveEffectiveLimit verifies the precedence server > smallest tag >
// global, and the unlimited fallback when nothing is configured.
func TestResolveEffectiveLimit(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	serverRepo := servers.NewSQLRepository(runtime.SQL)
	srv, err := serverRepo.Create(ctx, servers.Server{
		Name:               "eff-host",
		Host:               "203.0.113.30",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
		Tags:               []string{"hetzner", "customer-a"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	repo := analytics.NewSQLRepository(runtime.SQL)
	const gib = int64(1024) * 1024 * 1024

	// Nothing configured anywhere → unlimited.
	if _, _, ok, err := repo.ResolveEffectiveLimit(ctx, srv.ID, srv.Tags); err != nil || ok {
		t.Fatalf("Resolve() with nothing = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Global default applies when no tag/server cap exists.
	if err := repo.SetScopedLimit(ctx, analytics.LimitScopeGlobal, "", 500*gib); err != nil {
		t.Fatalf("SetScopedLimit(global) error = %v", err)
	}
	limit, source, ok, err := repo.ResolveEffectiveLimit(ctx, srv.ID, srv.Tags)
	if err != nil || !ok || limit != 500*gib || source != "global" {
		t.Fatalf("Resolve() global = (%d, %q, %v, %v), want (%d, global, true, nil)", limit, source, ok, err, 500*gib)
	}

	// A tag cap beats the global default, and the SMALLEST tag wins.
	if err := repo.SetScopedLimit(ctx, analytics.LimitScopeTag, "hetzner", 300*gib); err != nil {
		t.Fatalf("SetScopedLimit(hetzner) error = %v", err)
	}
	if err := repo.SetScopedLimit(ctx, analytics.LimitScopeTag, "customer-a", 200*gib); err != nil {
		t.Fatalf("SetScopedLimit(customer-a) error = %v", err)
	}
	limit, source, ok, err = repo.ResolveEffectiveLimit(ctx, srv.ID, srv.Tags)
	if err != nil || !ok || limit != 200*gib || source != "tag:customer-a" {
		t.Fatalf("Resolve() tag = (%d, %q, %v, %v), want (%d, tag:customer-a, true, nil)", limit, source, ok, err, 200*gib)
	}

	// A per-server cap beats everything.
	if err := repo.SetTrafficLimit(ctx, srv.ID, 50*gib); err != nil {
		t.Fatalf("SetTrafficLimit() error = %v", err)
	}
	limit, source, ok, err = repo.ResolveEffectiveLimit(ctx, srv.ID, srv.Tags)
	if err != nil || !ok || limit != 50*gib || source != "server" {
		t.Fatalf("Resolve() server = (%d, %q, %v, %v), want (%d, server, true, nil)", limit, source, ok, err, 50*gib)
	}
}
