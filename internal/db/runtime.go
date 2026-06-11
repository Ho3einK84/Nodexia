package db

import (
	"context"
	"database/sql"
	"fmt"
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

	conn.SetMaxOpenConns(cfg.MaxOpenConns)
	conn.SetMaxIdleConns(cfg.MaxIdleConns)
	conn.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := conn.PingContext(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("db: ping connection: %w", err)
	}

	migrator, err := NewBootstrapMigrator()
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

	return &Runtime{
		SQL:      conn,
		Dialect:  dialect,
		Migrator: migrator,
	}, nil
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
