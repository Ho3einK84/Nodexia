package db

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

type Dialect interface {
	Name() string
	DriverName() string
	DataSourceName(cfg config.DatabaseConfig) (string, error)
	Prepare(cfg config.DatabaseConfig) error
	// ConfigurePool applies dialect-appropriate connection-pool limits. SQLite
	// and MySQL want very different sizing, so the decision lives with the
	// dialect rather than in shared bootstrap code.
	ConfigurePool(conn *sql.DB, cfg config.DatabaseConfig)
}

type sqliteDialect struct{}

func (sqliteDialect) Name() string {
	return config.DriverSQLite
}

func (sqliteDialect) DriverName() string {
	return "sqlite"
}

func (sqliteDialect) DataSourceName(cfg config.DatabaseConfig) (string, error) {
	path := strings.TrimSpace(cfg.SQLitePath)
	if path == "" {
		return "", errors.New("db: sqlite path cannot be empty")
	}

	// Connection pragmas are encoded into the DSN so modernc.org/sqlite applies
	// them to EVERY connection the pool opens. The driver runs each `_pragma`
	// value as a `PRAGMA ...` statement inside newConn on every Open (see
	// applyQueryParams), which side-steps the classic database/sql footgun where
	// a one-off `PRAGMA` only configures whichever connection happened to run it.
	params := url.Values{}
	// busy_timeout: wait up to 5s for a contended lock instead of failing
	// immediately with SQLITE_BUSY. This is what lets a background scheduler
	// write queue behind an in-flight HTTP write rather than erroring and
	// cascading into HTTP 500s.
	params.Add("_pragma", "busy_timeout(5000)")
	// WAL: readers no longer block on the single writer (and vice versa), so
	// HTTP dashboard reads run concurrently with the scheduler's writes.
	params.Add("_pragma", "journal_mode(WAL)")
	// synchronous=NORMAL is the journal mode WAL is designed for: durable across
	// an application crash, with only the most recent transaction at risk on an
	// OS/power-level crash — a sound trade for a control panel and much cheaper
	// than FULL's per-commit fsync.
	params.Add("_pragma", "synchronous(NORMAL)")
	// foreign_keys: SQLite disables FK enforcement per-connection by default;
	// keep it on (previously handled by a global connection hook).
	params.Add("_pragma", "foreign_keys(ON)")
	// _txlock=immediate begins write transactions with BEGIN IMMEDIATE so the
	// busy_timeout handler covers lock acquisition. A DEFERRED transaction that
	// starts reading and later upgrades to a write returns SQLITE_BUSY
	// immediately (the busy handler is skipped to avoid deadlock) — the subtle
	// reason WAL + busy_timeout alone are not enough for the scheduler's
	// DELETE+INSERT replace transactions.
	params.Set("_txlock", "immediate")

	return filepath.Clean(path) + "?" + params.Encode(), nil
}

// ConfigurePool pins SQLite to a single connection. SQLite serializes writes at
// the database level, so opening many connections only manufactures
// writer-vs-writer contention — the exact SQLITE_BUSY failures this addresses.
// A single connection lets database/sql queue all access in-process so writes
// never collide, while WAL (set in the DSN) keeps reads off the writer's lock.
// The connection is kept warm (no idle eviction, no max lifetime) so the WAL and
// pragma setup is paid once rather than on every reconnect.
func (sqliteDialect) ConfigurePool(conn *sql.DB, _ config.DatabaseConfig) {
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)
}

func (sqliteDialect) Prepare(cfg config.DatabaseConfig) error {
	dbPath := filepath.Clean(strings.TrimSpace(cfg.SQLitePath))
	directory := filepath.Dir(dbPath)
	if directory == "." || directory == "" {
		return nil
	}

	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("db: create sqlite directory %q: %w", directory, err)
	}

	return nil
}

type mysqlDialect struct{}

func (mysqlDialect) Name() string {
	return config.DriverMySQL
}

func (mysqlDialect) DriverName() string {
	return "mysql"
}

func (mysqlDialect) DataSourceName(cfg config.DatabaseConfig) (string, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return "", errors.New("db: mysql dsn cannot be empty")
	}

	return dsn, nil
}

func (mysqlDialect) Prepare(config.DatabaseConfig) error {
	return nil
}

// ConfigurePool applies the configured pool sizing. MySQL handles concurrent
// connections natively, so its pool behaviour is left exactly as configured.
func (mysqlDialect) ConfigurePool(conn *sql.DB, cfg config.DatabaseConfig) {
	conn.SetMaxOpenConns(cfg.MaxOpenConns)
	conn.SetMaxIdleConns(cfg.MaxIdleConns)
	conn.SetConnMaxLifetime(cfg.ConnMaxLifetime)
}

func ResolveDialect(driver string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case config.DriverSQLite:
		return sqliteDialect{}, nil
	case config.DriverMySQL:
		return mysqlDialect{}, nil
	default:
		return nil, fmt.Errorf("db: unsupported database driver %q", driver)
	}
}
