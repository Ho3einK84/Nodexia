package db_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
)

// TestSQLitePragmasApplied verifies the DSN-encoded pragmas actually take effect
// on a live pooled connection. This is the guard against the common footgun of
// "thinking" WAL/busy_timeout are on when the DSN was built wrong — the assertion
// reads them back from the database rather than trusting the configuration.
func TestSQLitePragmasApplied(t *testing.T) {
	cfg := config.DatabaseConfig{
		Driver:     config.DriverSQLite,
		SQLitePath: filepath.Join(t.TempDir(), "pragmas.sqlite3"),
	}

	runtime, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer runtime.Close()

	ctx := context.Background()

	var journalMode string
	if err := runtime.SQL.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := runtime.SQL.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}

	var foreignKeys int
	if err := runtime.SQL.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var synchronous int
	if err := runtime.SQL.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("read synchronous: %v", err)
	}
	if synchronous != 1 { // 1 == NORMAL
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
}
