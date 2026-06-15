package servers_test

import (
	"context"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

func TestSQLRepositoryCRUD(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	repo := servers.NewSQLRepository(runtime.SQL)
	ctx := context.Background()

	created, err := repo.Create(ctx, servers.Server{
		Name:               "lab-1",
		Host:               "10.10.0.2",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
		Tags:               []string{"lab"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID < 1 {
		t.Fatalf("Create() id = %d", created.ID)
	}

	fetched, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if fetched.Name != "lab-1" || len(fetched.Tags) != 1 || fetched.Tags[0] != "lab" {
		t.Fatalf("GetByID() = %#v", fetched)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1", len(list))
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err = repo.GetByID(ctx, created.ID)
	if err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestSQLRepositoryUpdateCountry(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	repo := servers.NewSQLRepository(runtime.SQL)
	ctx := context.Background()

	created, err := repo.Create(ctx, servers.Server{
		Name:               "geo-1",
		Host:               "203.0.113.10",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "ubuntu",
		CredentialStrategy: servers.CredentialStrategyStored,
		CredentialRef:      "secret",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A fresh server has no detected country yet.
	if created.CountryCode != "" || created.CountryName != "" || !created.CountryCheckedAt.IsZero() {
		t.Fatalf("new server should have empty country, got %#v", created)
	}

	if err := repo.UpdateCountry(ctx, created.ID, "US", "United States"); err != nil {
		t.Fatalf("UpdateCountry() error = %v", err)
	}

	fetched, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if fetched.CountryCode != "US" || fetched.CountryName != "United States" {
		t.Fatalf("country not persisted, got %#v", fetched)
	}
	if fetched.CountryCheckedAt.IsZero() {
		t.Fatal("country_checked_at should be stamped after UpdateCountry")
	}
	// UpdateCountry must not disturb identity/credential fields.
	if fetched.Host != "203.0.113.10" || fetched.CredentialRef != "secret" {
		t.Fatalf("UpdateCountry altered unrelated fields: %#v", fetched)
	}

	// An empty result records "checked, nothing detected" without erroring.
	if err := repo.UpdateCountry(ctx, created.ID, "", ""); err != nil {
		t.Fatalf("UpdateCountry(empty) error = %v", err)
	}
	cleared, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if cleared.CountryCode != "" || cleared.CountryName != "" {
		t.Fatalf("empty UpdateCountry should clear country, got %#v", cleared)
	}
	if cleared.CountryCheckedAt.IsZero() {
		t.Fatal("country_checked_at should still be stamped after empty UpdateCountry")
	}

	// A missing server is a no-op, not an error.
	if err := repo.UpdateCountry(ctx, 999999, "GB", "United Kingdom"); err != nil {
		t.Fatalf("UpdateCountry(missing) error = %v", err)
	}
}
