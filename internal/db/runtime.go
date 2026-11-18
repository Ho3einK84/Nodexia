package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

type Runtime struct {
	SQL      *sql.DB
	Dialect  Dialect
	Migrator BootstrapMigrator
}

func Open(ctx context.Context, cfg config.DatabaseConfig) (*Runtime, error) {
	dialect, err := ResolveDialect(cfg.Driver)
	if err != nil {
		return nil, err
	}

	if err := dialect.Prepare(cfg); err != nil {
		return nil, err
	}

	dsn, err := dialect.DataSourceName(cfg)
	if err != nil {
		return nil, err
	}

	conn, err := sql.Open(dialect.DriverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open connection: %w", err)
	}

	dialect.ConfigurePool(conn, cfg)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := conn.PingContext(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("db: ping connection: %w", err)
	}

	migrator, err := NewBootstrapMigratorFor(dialect)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	migrationCtx, migrationCancel := context.WithTimeout(ctx, 10*time.Second)
	defer migrationCancel()

	if err := migrator.Apply(migrationCtx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	if dialect.Name() == config.DriverSQLite {
		hardenSQLiteFilePermissions(cfg.SQLitePath)
	}

	return &Runtime{
		SQL:      conn,
		Dialect:  dialect,
		Migrator: migrator,
	}, nil
}

func hardenSQLiteFilePermissions(rawPath string) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || trimmed == ":memory:" || strings.HasPrefix(trimmed, "file::memory:") || strings.Contains(trimmed, "mode=memory") {
		return
	}
	cleanPath := filepath.Clean(trimmed)
	for _, candidate := range []string{cleanPath, cleanPath + "-wal", cleanPath + "-shm"} {
		if _, err := os.Stat(candidate); err == nil {
			_ = os.Chmod(candidate, 0o600)
		}
	}
}

func (r *Runtime) Close() error {
	if r == nil || r.SQL == nil {
		return nil
	}

	return r.SQL.Close()
}

func (r *Runtime) MigrationCount() int {
	if r == nil {
		return 0
	}

	return len(r.Migrator.Migrations())
}
