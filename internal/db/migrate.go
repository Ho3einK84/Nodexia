package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	assets "github.com/Ho3einK84/Nodexia"
)

type Migration struct {
	ID          string
	Description string
	SQL         string
}

type BootstrapMigrator struct {
	migrations []Migration
}

// migrationTableDDL is written in a portable subset so it applies before any
// dialect translation runs. id is a short VARCHAR (the keys are "bootstrap-NN"),
// keeping it within the InnoDB key length on every MySQL/MariaDB version while
// remaining plain TEXT affinity on SQLite.
const migrationTableDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  id VARCHAR(191) PRIMARY KEY,
  description TEXT NOT NULL,
  applied_at DATETIME NOT NULL
);`

// NewBootstrapMigrator builds the migrator for the SQLite dialect (the canonical
// schema is already valid SQLite). It is kept for callers/tests that do not need
// a specific engine.
func NewBootstrapMigrator() (BootstrapMigrator, error) {
	return NewBootstrapMigratorFor(sqliteDialect{})
}

// NewBootstrapMigratorFor builds the migrator for a specific dialect, translating
// each canonical statement through dialect.TranslateDDL so the same schema.sql
// drives SQLite and MySQL. The statement COUNT and ORDER are identical across
// dialects (translation is 1:1), so the bootstrap-NN ids stay stable.
func NewBootstrapMigratorFor(dialect Dialect) (BootstrapMigrator, error) {
	statements := splitStatements(assets.Schema())
	migrations := make([]Migration, 0, len(statements))

	for index, statement := range statements {
		if dialect != nil {
			statement = dialect.TranslateDDL(statement)
		}
		migrations = append(migrations, Migration{
			ID:          fmt.Sprintf("bootstrap-%02d", index+1),
			Description: "initial schema statement",
			SQL:         statement,
		})
	}

	return BootstrapMigrator{migrations: migrations}, nil
}

func BootstrapMigrationCount() int {
	return len(splitStatements(assets.Schema()))
}

func (m BootstrapMigrator) Migrations() []Migration {
	out := make([]Migration, len(m.migrations))
	copy(out, m.migrations)
	return out
}

func (m BootstrapMigrator) Apply(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, migrationTableDDL); err != nil {
		return fmt.Errorf("db: ensure schema_migrations table: %w", err)
	}

	for _, migration := range m.migrations {
		applied, err := migrationApplied(ctx, conn, migration.ID)
		if err != nil {
			return fmt.Errorf("db: check migration %s: %w", migration.ID, err)
		}

		if applied {
			continue
		}

		if err := applyMigration(ctx, conn, migration); err != nil {
			return err
		}
	}

	return nil
}

func migrationApplied(ctx context.Context, queryer DBTX, id string) (bool, error) {
	var applied int
	if err := queryer.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE id = ? LIMIT 1", id).Scan(&applied); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func applyMigration(ctx context.Context, conn *sql.DB, migration Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin migration %s: %w", migration.ID, err)
	}

	for _, stmt := range splitStatements(migration.SQL) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("db: apply migration %s: %w", migration.ID, err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO schema_migrations (id, description, applied_at) VALUES (?, ?, ?)",
		migration.ID,
		migration.Description,
		time.Now().UTC(),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("db: record migration %s: %w", migration.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit migration %s: %w", migration.ID, err)
	}

	return nil
}

// splitStatements splits SQL text into individual statements at semicolons,
// ignoring semicolons inside single-quoted string literals and line comments.
func splitStatements(schema string) []string {
	var statements []string
	var buf strings.Builder
	inString := false
	i := 0
	for i < len(schema) {
		ch := schema[i]
		switch {
		case inString:
			buf.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(schema) && schema[i+1] == '\'' {
					i++
					buf.WriteByte(schema[i])
				} else {
					inString = false
				}
			}
		case ch == '\'':
			inString = true
			buf.WriteByte(ch)
		case ch == '-' && i+1 < len(schema) && schema[i+1] == '-':
			for i < len(schema) && schema[i] != '\n' {
				i++
			}
			continue
		case ch == ';':
			stmt := strings.TrimSpace(buf.String())
			if stmt != "" {
				statements = append(statements, stmt+";")
			}
			buf.Reset()
		default:
			buf.WriteByte(ch)
		}
		i++
	}
	if stmt := strings.TrimSpace(buf.String()); stmt != "" {
		statements = append(statements, stmt+";")
	}
	return statements
}
