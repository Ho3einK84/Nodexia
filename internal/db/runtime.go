package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

type Runtime struct {
	SQL      *sql.DB
	ReadSQL  *sql.DB // concurrent read-only WAL pool on file-backed SQLite; equals SQL otherwise
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

	readConn := conn
	if dialect.Name() == config.DriverSQLite {
		hardenSQLiteFilePermissions(cfg.SQLitePath)
		if readDSN, ok := sqliteReadDataSourceName(cfg); ok {
			rc, openErr := sql.Open(dialect.DriverName(), readDSN)
			if openErr != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("db: open sqlite reader pool: %w", openErr)
			}
			configureSQLiteReadPool(rc)
			rPingCtx, rCancel := context.WithTimeout(ctx, 5*time.Second)
			pingErr := rc.PingContext(rPingCtx)
			rCancel()
			if pingErr != nil {
				_ = rc.Close()
				_ = conn.Close()
				return nil, fmt.Errorf("db: ping sqlite reader pool: %w", pingErr)
			}
			readConn = rc
		}
	}

	return &Runtime{
		SQL:      conn,
		ReadSQL:  readConn,
		Dialect:  dialect,
		Migrator: migrator,
	}, nil
}

// Reader returns the concurrent read-only connection pool when available (on
// file-backed SQLite WAL), falling back to the primary SQL pool on MySQL or
// in-memory SQLite databases.
func (r *Runtime) Reader() *sql.DB {
	if r == nil {
		return nil
	}
	if r.ReadSQL != nil {
		return r.ReadSQL
	}
	return r.SQL
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
	if r == nil {
		return nil
	}

	var combined error
	if r.ReadSQL != nil && r.ReadSQL != r.SQL {
		if err := r.ReadSQL.Close(); err != nil {
			combined = errors.Join(combined, err)
		}
	}
	if r.SQL != nil {
		if err := r.SQL.Close(); err != nil {
			combined = errors.Join(combined, err)
		}
	}
	return combined
}

func (r *Runtime) MigrationCount() int {
	if r == nil {
		return 0
	}

	return r.Migrator.Count()
}
