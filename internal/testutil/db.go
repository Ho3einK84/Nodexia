package testutil

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
)

// OpenTestDB opens an isolated SQLite runtime with migrations applied.
func OpenTestDB(t *testing.T) *db.Runtime {
	t.Helper()

	cfg := TestConfig(t)
	cfg.Database.SQLitePath = filepath.Join(t.TempDir(), "nodexia.test.sqlite3")

	runtime, err := db.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		_ = runtime.Close()
	})

	return runtime
}

// TestDatabaseConfig returns sqlite settings for a temp database file.
func TestDatabaseConfig(t *testing.T) config.DatabaseConfig {
	t.Helper()
	cfg := TestConfig(t)
	cfg.Database.SQLitePath = filepath.Join(t.TempDir(), "nodexia.test.sqlite3")
	return cfg.Database
}
