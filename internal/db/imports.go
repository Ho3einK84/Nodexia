package db

import (
	"context"

	_ "github.com/go-sql-driver/mysql"
	sqlite "modernc.org/sqlite"
)

func init() {
	// Enable foreign-key enforcement on every SQLite connection.
	// Without this, SQLite silently ignores all FK constraints.
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
		_, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON", nil)
		return err
	})
}
