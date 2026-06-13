package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("monitoring: not found")

type Snapshot struct {
	ID             int64
	ServerID       int64
	ServerName     string
	CPUUsage       float64
	RAMUsage       float64
	SwapUsage      float64
	DiskUsage      float64
	LoadAverage1   float64
	LoadAverage5   float64
	LoadAverage15  float64
	UptimeSeconds  int64
	NetworkSummary string
	CreatedAt      time.Time
}

type Repository interface {
	Append(ctx context.Context, snapshot Snapshot) (Snapshot, error)
	HasAny(ctx context.Context, serverID int64) (bool, error)
	GetLatestByServer(ctx context.Context, serverID int64) (Snapshot, error)
	ListLatestByServer(ctx context.Context, limit int) ([]Snapshot, error)
}

type SQLRepository struct {
	conn *sql.DB
}

func NewSQLRepository(conn *sql.DB) SQLRepository {
	return SQLRepository{conn: conn}
}

func (r SQLRepository) Append(ctx context.Context, snapshot Snapshot) (Snapshot, error) {
	snapshot = normalizeSnapshot(snapshot)
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}

	result, err := r.conn.ExecContext(
		ctx,
		`INSERT INTO system_snapshots (
			server_id,
			cpu_usage,
			ram_usage,
			swap_usage,
			disk_usage,
			load_average_1,
			load_average_5,
			load_average_15,
			uptime_seconds,
			network_summary,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ServerID,
		snapshot.CPUUsage,
		snapshot.RAMUsage,
		snapshot.SwapUsage,
		snapshot.DiskUsage,
		snapshot.LoadAverage1,
		snapshot.LoadAverage5,
		snapshot.LoadAverage15,
		snapshot.UptimeSeconds,
		snapshot.NetworkSummary,
		snapshot.CreatedAt,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("monitoring: append snapshot: %w", err)
	}

	snapshot.ID, err = result.LastInsertId()
	if err != nil {
		return Snapshot{}, fmt.Errorf("monitoring: read snapshot last insert id: %w", err)
	}

	return snapshot, nil
}

func (r SQLRepository) HasAny(ctx context.Context, serverID int64) (bool, error) {
	var count int
	if err := r.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_snapshots WHERE server_id = ?`, serverID).Scan(&count); err != nil {
		return false, fmt.Errorf("monitoring: check snapshots for server %d: %w", serverID, err)
	}
	return count > 0, nil
}

func (r SQLRepository) GetLatestByServer(ctx context.Context, serverID int64) (Snapshot, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT ss.id, ss.server_id, s.name, ss.cpu_usage, ss.ram_usage, ss.swap_usage, ss.disk_usage, ss.load_average_1, ss.load_average_5, ss.load_average_15, ss.uptime_seconds, ss.network_summary, ss.created_at
		 FROM system_snapshots ss
		 JOIN servers s ON s.id = ss.server_id
		 WHERE ss.server_id = ?
		 ORDER BY ss.id DESC
		 LIMIT 1`,
		serverID,
	)

	snapshot, err := scanSnapshot(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, fmt.Errorf("monitoring: get latest snapshot for server %d: %w", serverID, err)
	}

	return snapshot, nil
}

func (r SQLRepository) ListLatestByServer(ctx context.Context, limit int) ([]Snapshot, error) {
	if limit <= 0 {
		limit = 6
	}

	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT ss.id, ss.server_id, s.name, ss.cpu_usage, ss.ram_usage, ss.swap_usage, ss.disk_usage, ss.load_average_1, ss.load_average_5, ss.load_average_15, ss.uptime_seconds, ss.network_summary, ss.created_at
		 FROM system_snapshots ss
		 JOIN servers s ON s.id = ss.server_id
		 JOIN (
		   SELECT server_id, MAX(id) AS latest_id
		   FROM system_snapshots
		   GROUP BY server_id
		 ) latest ON latest.latest_id = ss.id
		 ORDER BY ss.id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("monitoring: list latest snapshots: %w", err)
	}
	defer rows.Close()

	snapshots := make([]Snapshot, 0)
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("monitoring: scan latest snapshot row: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("monitoring: iterate latest snapshots: %w", err)
	}

	return snapshots, nil
}

func (r SQLRepository) ListAllLatestByServer(ctx context.Context) ([]Snapshot, error) {
	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT ss.id, ss.server_id, s.name, ss.cpu_usage, ss.ram_usage, ss.swap_usage, ss.disk_usage, ss.load_average_1, ss.load_average_5, ss.load_average_15, ss.uptime_seconds, ss.network_summary, ss.created_at
		 FROM system_snapshots ss
		 JOIN servers s ON s.id = ss.server_id
		 JOIN (
		   SELECT server_id, MAX(id) AS latest_id
		   FROM system_snapshots
		   GROUP BY server_id
		 ) latest ON latest.latest_id = ss.id
		 ORDER BY ss.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("monitoring: list all latest snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []Snapshot
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("monitoring: scan latest snapshot row: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}

	return snapshots, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSnapshot(scanner rowScanner) (Snapshot, error) {
	var snapshot Snapshot
	var createdAtRaw any
	if err := scanner.Scan(
		&snapshot.ID,
		&snapshot.ServerID,
		&snapshot.ServerName,
		&snapshot.CPUUsage,
		&snapshot.RAMUsage,
		&snapshot.SwapUsage,
		&snapshot.DiskUsage,
		&snapshot.LoadAverage1,
		&snapshot.LoadAverage5,
		&snapshot.LoadAverage15,
		&snapshot.UptimeSeconds,
		&snapshot.NetworkSummary,
		&createdAtRaw,
	); err != nil {
		return Snapshot{}, err
	}

	createdAt, err := parseDatabaseTime(createdAtRaw)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse created_at: %w", err)
	}
	snapshot.CreatedAt = createdAt
	return snapshot, nil
}

func normalizeSnapshot(snapshot Snapshot) Snapshot {
	snapshot.NetworkSummary = strings.TrimSpace(snapshot.NetworkSummary)
	return snapshot
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
