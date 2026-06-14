package db

import (
	"context"
	"errors"
	"time"
)

// SQLite result codes for transient lock contention. Declared locally so this
// package does not take a hard dependency on the driver's internal lib package;
// they are stable, standard SQLite codes.
const (
	sqliteBusy   = 5 // SQLITE_BUSY: the database file is locked.
	sqliteLocked = 6 // SQLITE_LOCKED: a table in the database is locked.
)

// IsBusy reports whether err is a transient SQLite contention error
// (SQLITE_BUSY / SQLITE_LOCKED) that is safe to retry. It matches any error
// exposing a `Code() int` method (modernc.org/sqlite's *Error does), so it
// needs no import of the driver package. MySQL never produces these codes, so
// shared write paths can be wrapped unconditionally without affecting MySQL.
func IsBusy(err error) bool {
	if err == nil {
		return false
	}

	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		switch coder.Code() {
		case sqliteBusy, sqliteLocked:
			return true
		}
	}
	return false
}

// RetryOnBusy runs fn, retrying a few times with a short escalating backoff
// while it returns a transient SQLite busy/locked error. The busy_timeout pragma
// already makes the driver wait for contended locks, so this is defense-in-depth
// for the rare residual case (e.g. a WAL checkpoint racing a write) rather than
// the primary fix. For non-busy errors it returns immediately, and because
// IsBusy never matches MySQL errors it is a transparent passthrough there.
func RetryOnBusy(ctx context.Context, fn func() error) error {
	const maxAttempts = 4

	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = fn(); !IsBusy(err) {
			return err
		}

		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	return err
}
