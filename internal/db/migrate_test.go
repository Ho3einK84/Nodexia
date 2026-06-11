package db_test

import (
	"context"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

func TestBootstrapMigratorIsIdempotent(t *testing.T) {
	runtime := testutil.OpenTestDB(t)

	migrator, err := db.NewBootstrapMigrator()
	if err != nil {
		t.Fatalf("NewBootstrapMigrator() error = %v", err)
	}

	ctx := context.Background()
	if err := migrator.Apply(ctx, runtime.SQL); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if err := migrator.Apply(ctx, runtime.SQL); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}

	var tableCount int
	if err := runtime.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tableCount); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tableCount < 5 {
		t.Fatalf("tableCount = %d, expected core schema tables", tableCount)
	}

	var migrationCount int
	if err := runtime.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount < 1 {
		t.Fatal("expected schema_migrations entries")
	}
}

func TestBootstrapMigrationCountMatchesMigrator(t *testing.T) {
	migrator, err := db.NewBootstrapMigrator()
	if err != nil {
		t.Fatalf("NewBootstrapMigrator() error = %v", err)
	}

	if got := len(migrator.Migrations()); got != db.BootstrapMigrationCount() {
		t.Fatalf("migration count = %d, BootstrapMigrationCount() = %d", got, db.BootstrapMigrationCount())
	}
}
