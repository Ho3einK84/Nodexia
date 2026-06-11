package db

import (
	"errors"
	"fmt"
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

	return filepath.Clean(path), nil
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
