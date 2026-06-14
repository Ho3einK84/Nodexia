package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
)

// TestSQLiteConcurrentWritesAndReads reproduces the scheduler-vs-HTTP contention
// that previously surfaced as "database is locked (5) (SQLITE_BUSY)". Many
// goroutines run replace-latest style write transactions (DELETE + INSERT, like
// nodes.ReplaceLatest) while others read concurrently, all through a runtime
// opened by db.Open so the real DSN pragmas and pool sizing are exercised.
//
// With WAL + busy_timeout + the single-writer SQLite pool this completes without
// any busy/locked error. Under the previous configuration — a bare file-path DSN
// (rollback journal, no busy_timeout) with MaxOpenConns=10 — it fails
// intermittently, so this test would have caught the regression.
func TestSQLiteConcurrentWritesAndReads(t *testing.T) {
	cfg := config.DatabaseConfig{
		Driver:     config.DriverSQLite,
		SQLitePath: filepath.Join(t.TempDir(), "concurrency.sqlite3"),
		// Deliberately request the old, contention-prone pool size to prove the
		// sqlite dialect overrides it to a single writer.
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
	}

	runtime, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer runtime.Close()

	ctx := context.Background()
	if _, err := runtime.SQL.ExecContext(ctx, `CREATE TABLE latest_snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id INTEGER NOT NULL,
		payload TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	const (
		writers      = 8
		readers      = 8
		iterations   = 40
		bufferedSlot = (writers + readers)
	)

	var wg sync.WaitGroup
	errCh := make(chan error, bufferedSlot)
	start := make(chan struct{})

	// Writers: scheduler-style replace-latest transactions, each owning its own
	// server_id so they continually delete and re-insert the same row set.
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(serverID int64) {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if err := replaceLatest(ctx, runtime.SQL, serverID, i); err != nil {
					errCh <- fmt.Errorf("writer %d iter %d: %w", serverID, i, err)
					return
				}
			}
		}(int64(w))
	}

	// Readers: concurrent dashboard-style reads against the same table.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				var count int
				if err := runtime.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM latest_snapshots`).Scan(&count); err != nil {
					errCh <- fmt.Errorf("reader iter %d: %w", i, err)
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if db.IsBusy(err) {
			t.Fatalf("hit SQLite busy/locked under concurrency (the bug): %v", err)
		}
		t.Fatalf("unexpected error under concurrency: %v", err)
	}
}

// replaceLatest mirrors nodes.ReplaceLatest: a single write transaction that
// clears the server's rows and inserts a fresh one.
func replaceLatest(ctx context.Context, conn *sql.DB, serverID int64, n int) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM latest_snapshots WHERE server_id = ?`, serverID); err != nil {
		_ = tx.Rollback()
		return err
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO latest_snapshots (server_id, payload, created_at) VALUES (?, ?, ?)`,
		serverID,
		fmt.Sprintf("payload-%d", n),
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}
