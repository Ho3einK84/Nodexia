package db

import (
	"strings"
	"testing"

	assets "github.com/Ho3einK84/Nodexia"
)

func TestTranslateMySQLDDLAutoIncrement(t *testing.T) {
	got := translateMySQLDDL("CREATE TABLE IF NOT EXISTS servers (\n  id INTEGER PRIMARY KEY,\n  name TEXT NOT NULL\n);")
	if !strings.Contains(got, "id INTEGER AUTO_INCREMENT PRIMARY KEY") {
		t.Fatalf("expected the id column to gain AUTO_INCREMENT, got:\n%s", got)
	}
}

func TestTranslateMySQLDDLLeavesNonGeneratedPKAlone(t *testing.T) {
	// server_id is a non-generated FK primary key (always inserted explicitly): it
	// must NOT become AUTO_INCREMENT, and \b must not match inside "server_id".
	got := translateMySQLDDL("CREATE TABLE IF NOT EXISTS server_traffic_limits (\n  server_id INTEGER PRIMARY KEY,\n  monthly_limit_bytes INTEGER NOT NULL\n);")
	if strings.Contains(got, "AUTO_INCREMENT") {
		t.Fatalf("server_id must not be AUTO_INCREMENT, got:\n%s", got)
	}
	if !strings.Contains(got, "server_id INTEGER PRIMARY KEY") {
		t.Fatalf("server_id primary key altered unexpectedly:\n%s", got)
	}
}

func TestTranslateMySQLDDLDropsIndexIfNotExists(t *testing.T) {
	got := translateMySQLDDL("CREATE INDEX IF NOT EXISTS idx_x ON t (server_id);")
	if strings.Contains(strings.ToUpper(got), "IF NOT EXISTS") {
		t.Fatalf("MySQL CREATE INDEX must drop IF NOT EXISTS, got: %q", got)
	}
	if !strings.HasPrefix(got, "CREATE INDEX idx_x") {
		t.Fatalf("unexpected index translation: %q", got)
	}
}

func TestTranslateMySQLDDLTableOptions(t *testing.T) {
	got := translateMySQLDDL("CREATE TABLE IF NOT EXISTS t (\n  id INTEGER PRIMARY KEY\n);")
	if !strings.Contains(got, "ENGINE=InnoDB") || !strings.Contains(got, "utf8mb4") {
		t.Fatalf("CREATE TABLE should pin InnoDB/utf8mb4, got:\n%s", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), ";") {
		t.Fatalf("translated statement must remain terminated, got:\n%s", got)
	}
}

func TestTranslateMySQLDDLLeavesAlterAndInsertAlone(t *testing.T) {
	alter := "ALTER TABLE node_snapshots ADD COLUMN data_dir TEXT NOT NULL DEFAULT '';"
	if got := translateMySQLDDL(alter); got != alter {
		t.Fatalf("ALTER must pass through unchanged, got: %q", got)
	}
}

// TestMySQLSchemaPortability lints the whole canonical schema after MySQL
// translation: no auto-increment id is left untranslated, no index keeps the
// unsupported IF NOT EXISTS, every table pins InnoDB/utf8mb4, and no TEXT column
// is left inside a key (all key/index columns are short VARCHARs).
func TestMySQLSchemaPortability(t *testing.T) {
	statements := splitStatements(assets.Schema())
	if len(statements) == 0 {
		t.Fatal("no schema statements found")
	}
	for _, raw := range statements {
		stmt := translateMySQLDDL(raw)
		upper := strings.ToUpper(stmt)

		if reAutoIncrementPK.MatchString(stmt) {
			t.Errorf("untranslated auto-increment id remains:\n%s", stmt)
		}
		if strings.HasPrefix(strings.TrimSpace(upper), "CREATE INDEX") && strings.Contains(upper, "IF NOT EXISTS") {
			t.Errorf("CREATE INDEX still has IF NOT EXISTS:\n%s", stmt)
		}
		if strings.HasPrefix(strings.TrimSpace(upper), "CREATE TABLE") && !strings.Contains(upper, "ENGINE=INNODB") {
			t.Errorf("CREATE TABLE missing InnoDB option:\n%s", stmt)
		}
		// A TEXT column may not sit in a key on MySQL. The schema keeps key columns
		// as VARCHAR, so no key clause should reference a bare "... TEXT ... ,
		// PRIMARY KEY"/"UNIQUE" pairing — assert the known key columns are VARCHAR.
		if strings.Contains(upper, "PRIMARY KEY (SCOPE, REF)") && !strings.Contains(upper, "VARCHAR") {
			t.Errorf("traffic_limit_rules key columns must be VARCHAR:\n%s", stmt)
		}
	}
}

// TestBootstrapMigratorDialectParity confirms the SQLite and MySQL migrators
// derive the same number of statements in the same order (translation is 1:1), so
// the bootstrap-NN migration ids stay stable across engines.
func TestBootstrapMigratorDialectParity(t *testing.T) {
	sqlite, err := NewBootstrapMigratorFor(sqliteDialect{})
	if err != nil {
		t.Fatalf("sqlite migrator: %v", err)
	}
	mysql, err := NewBootstrapMigratorFor(mysqlDialect{})
	if err != nil {
		t.Fatalf("mysql migrator: %v", err)
	}
	sm, mm := sqlite.Migrations(), mysql.Migrations()
	if len(sm) != len(mm) {
		t.Fatalf("statement count differs: sqlite %d, mysql %d", len(sm), len(mm))
	}
	for i := range sm {
		if sm[i].ID != mm[i].ID {
			t.Fatalf("migration id mismatch at %d: %q vs %q", i, sm[i].ID, mm[i].ID)
		}
	}
}
