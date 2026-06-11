package commands

import (
	"context"
	"time"
)

type HistoryEntry struct {
	ID         int64
	ServerID   int64
	Command    string
	ExitCode   *int
	Stdout     string
	Stderr     string
	ExecutedAt time.Time
}

type Repository interface {
	Append(ctx context.Context, entry HistoryEntry) (HistoryEntry, error)
	ListByServer(ctx context.Context, serverID int64, limit int) ([]HistoryEntry, error)
}
