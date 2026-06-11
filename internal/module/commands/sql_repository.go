package commands

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const defaultHistoryLimit = 8

type SQLRepository struct {
	conn *sql.DB
}

func NewSQLRepository(conn *sql.DB) SQLRepository {
	return SQLRepository{conn: conn}
}

func (r SQLRepository) Append(ctx context.Context, entry HistoryEntry) (HistoryEntry, error) {
	entry.Command = strings.TrimSpace(entry.Command)
	entry.Stdout = strings.TrimSpace(entry.Stdout)
	entry.Stderr = strings.TrimSpace(entry.Stderr)
	if entry.ExecutedAt.IsZero() {
		entry.ExecutedAt = time.Now().UTC()
	}

	result, err := r.conn.ExecContext(
		ctx,
		`INSERT INTO command_history (server_id, command_text, exit_code, stdout, stderr, executed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ServerID,
		entry.Command,
		entry.ExitCode,
		entry.Stdout,
		entry.Stderr,
		entry.ExecutedAt,
	)
	if err != nil {
		return HistoryEntry{}, fmt.Errorf("commands: append history: %w", err)
	}

	entry.ID, err = result.LastInsertId()
	if err != nil {
		return HistoryEntry{}, fmt.Errorf("commands: read last insert id: %w", err)
	}

	return entry, nil
}

func (r SQLRepository) ListByServer(ctx context.Context, serverID int64, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = defaultHistoryLimit
	}

	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT id, server_id, command_text, exit_code, stdout, stderr, executed_at
		 FROM command_history
		 WHERE server_id = ?
		 ORDER BY id DESC
		 LIMIT ?`,
		serverID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("commands: list history for server %d: %w", serverID, err)
	}
	defer rows.Close()

	history := make([]HistoryEntry, 0, limit)
	for rows.Next() {
		var entry HistoryEntry
		var executedAtRaw any
		if err := rows.Scan(
			&entry.ID,
			&entry.ServerID,
			&entry.Command,
			&entry.ExitCode,
			&entry.Stdout,
			&entry.Stderr,
			&executedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("commands: scan history row: %w", err)
		}

		executedAt, err := parseDatabaseTime(executedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("commands: parse executed_at: %w", err)
		}

		entry.ExecutedAt = executedAt
		history = append(history, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("commands: iterate history rows: %w", err)
	}

	return history, nil
}

func parseDatabaseTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		return parseTimeString(typed)
	case []byte:
		return parseTimeString(string(typed))
	case nil:
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time type %T", value)
	}
}

func parseTimeString(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}

	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}

	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported time value %q", value)
}
