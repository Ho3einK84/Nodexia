package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// JobRun is one persisted background job outcome, shown on the diagnostics
// page so an operator can see what actually happened recently — not just the
// latest in-memory state.
type JobRun struct {
	ID         int64
	ServerID   int64
	ServerName string // joined from servers at read time; "" if the server is gone
	JobType    JobType
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
	Success    bool
	Message    string
	Error      string
}

// maxRetainedJobRuns caps the scheduler_job_runs table globally. Trimming
// happens after each insert, so the table can never grow unbounded and no
// scheduled cleanup is needed. At the default 15-minute monitoring interval
// this holds roughly a day of history for a ~10-server fleet.
const maxRetainedJobRuns = 1000

// jobRunsRepository persists job run outcomes. It lives in the scheduler
// package (not a module) because the runs describe scheduler behaviour, but it
// follows the modules' portable-SQL conventions.
type jobRunsRepository struct {
	conn *sql.DB
}

func newJobRunsRepository(conn *sql.DB) *jobRunsRepository {
	if conn == nil {
		return nil
	}
	return &jobRunsRepository{conn: conn}
}

// Record inserts one completed run and trims the table to the retention cap.
// A nil receiver (no database) is a safe no-op.
func (r *jobRunsRepository) Record(ctx context.Context, run JobRun) error {
	if r == nil || r.conn == nil {
		return nil
	}

	success := 0
	if run.Success {
		success = 1
	}
	if _, err := r.conn.ExecContext(ctx,
		`INSERT INTO scheduler_job_runs (server_id, job_type, started_at, finished_at, duration_ms, success, message, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ServerID,
		string(run.JobType),
		run.StartedAt.UTC(),
		run.FinishedAt.UTC(),
		run.Duration.Milliseconds(),
		success,
		run.Message,
		run.Error,
	); err != nil {
		return fmt.Errorf("scheduler: record job run: %w", err)
	}

	return r.trim(ctx)
}

// trim deletes rows beyond the global retention cap, oldest first. The cutoff
// id is read in a separate statement because MySQL cannot modify a table it is
// simultaneously selecting from in a subquery.
func (r *jobRunsRepository) trim(ctx context.Context) error {
	var cutoff int64
	err := r.conn.QueryRowContext(ctx,
		`SELECT id FROM scheduler_job_runs ORDER BY id DESC LIMIT 1 OFFSET ?`,
		maxRetainedJobRuns-1,
	).Scan(&cutoff)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // fewer rows than the cap — nothing to trim
		}
		return fmt.Errorf("scheduler: find job run cutoff: %w", err)
	}

	if _, err := r.conn.ExecContext(ctx,
		`DELETE FROM scheduler_job_runs WHERE id < ?`, cutoff,
	); err != nil {
		return fmt.Errorf("scheduler: trim job runs: %w", err)
	}
	return nil
}

// ListRecent returns the newest runs (most recent first), joined with the
// server name for display. A nil receiver returns no rows.
func (r *jobRunsRepository) ListRecent(ctx context.Context, limit int) ([]JobRun, error) {
	if r == nil || r.conn == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := r.conn.QueryContext(ctx,
		`SELECT j.id, j.server_id, COALESCE(s.name, ''), j.job_type, j.started_at, j.finished_at, j.duration_ms, j.success, j.message, j.error
		 FROM scheduler_job_runs j
		 LEFT JOIN servers s ON s.id = j.server_id
		 ORDER BY j.id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list job runs: %w", err)
	}
	defer rows.Close()

	var out []JobRun
	for rows.Next() {
		var run JobRun
		var jobType string
		var startedRaw, finishedRaw any
		var durationMS int64
		var success int
		if err := rows.Scan(&run.ID, &run.ServerID, &run.ServerName, &jobType, &startedRaw, &finishedRaw, &durationMS, &success, &run.Message, &run.Error); err != nil {
			return nil, fmt.Errorf("scheduler: scan job run: %w", err)
		}
		run.JobType = JobType(jobType)
		run.StartedAt = parseRunTime(startedRaw)
		run.FinishedAt = parseRunTime(finishedRaw)
		run.Duration = time.Duration(durationMS) * time.Millisecond
		run.Success = success == 1
		out = append(out, run)
	}
	return out, rows.Err()
}

// parseRunTime converts a driver-provided timestamp (time.Time on MySQL,
// string/[]byte on SQLite) into a UTC time; unparseable values map to zero.
func parseRunTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	case string:
		return parseRunTimeString(typed)
	case []byte:
		return parseRunTimeString(string(typed))
	default:
		return time.Time{}
	}
}

func parseRunTimeString(value string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
