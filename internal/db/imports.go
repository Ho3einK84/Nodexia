package db

import (
	// Register the database/sql drivers. SQLite connection setup (WAL,
	// busy_timeout, foreign_keys, …) is applied per-connection via the DSN built
	// in the sqlite dialect's DataSourceName, so no global connection hook is
	// needed here.
	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)
