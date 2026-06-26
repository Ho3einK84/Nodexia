package db

import (
	"regexp"
	"strings"
)

// MySQL DDL translation. The canonical schema in schema.sql is written in the
// SQLite dialect; these rewrites make each statement valid on MySQL/MariaDB
// without maintaining a second schema file (which would inevitably drift from the
// append-only canonical one).
var (
	// reAutoIncrementPK matches the generated integer primary key. Only the
	// standalone `id` column is auto-incremented: `\b` before "id" never matches
	// inside "server_id" (the char before "id" there is "_", a word character), so
	// `server_id INTEGER PRIMARY KEY` (a non-generated FK primary key, always
	// inserted explicitly) is correctly left without AUTO_INCREMENT.
	reAutoIncrementPK = regexp.MustCompile(`\bid\s+INTEGER\s+PRIMARY\s+KEY\b`)
	// reCreateIndexINE strips the IF NOT EXISTS clause MySQL does not accept on
	// CREATE INDEX. Bootstrap migration tracking already guarantees each index DDL
	// runs at most once, so a plain CREATE INDEX is safe.
	reCreateIndexINE = regexp.MustCompile(`(?i)CREATE\s+INDEX\s+IF\s+NOT\s+EXISTS`)
)

// translateMySQLDDL adapts a single SQLite-flavoured schema statement to MySQL.
func translateMySQLDDL(stmt string) string {
	out := reAutoIncrementPK.ReplaceAllString(stmt, "id INTEGER AUTO_INCREMENT PRIMARY KEY")
	out = reCreateIndexINE.ReplaceAllString(out, "CREATE INDEX")
	out = appendMySQLTableOptions(out)
	return out
}

// appendMySQLTableOptions pins InnoDB + utf8mb4 on CREATE TABLE so foreign keys
// are enforced and Unicode (e.g. Persian tags/notes) is stored correctly,
// regardless of the server's default charset. Non-CREATE-TABLE statements pass
// through untouched.
func appendMySQLTableOptions(stmt string) string {
	trimmed := strings.TrimSpace(stmt)
	if !strings.HasPrefix(strings.ToUpper(trimmed), "CREATE TABLE") {
		return stmt
	}
	body := strings.TrimRight(trimmed, "; \n\t")
	if !strings.HasSuffix(body, ")") {
		return stmt // unexpected shape; leave it untouched rather than corrupt it
	}
	return body + " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;"
}
