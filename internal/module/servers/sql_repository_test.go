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
