package system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("system: not found")

type FactSnapshot struct {
	ID             int64
	ServerID       int64
	Hostname       string
	OSName         string
	OSVersion      string
	KernelVersion  string
	Architecture   string
	CPUModel       string
	CPUCores       int64
	MemTotalKB     int64
	DiskTotalKB    int64
	UptimeSeconds  int64
	LastUpdateUnix int64
	CollectedAt    time.Time
}

type Repository interface {
	Append(ctx context.Context, snapshot FactSnapshot) (FactSnapshot, error)
	HasAny(ctx context.Context, serverID int64) (bool, error)
	GetLatestByServer(ctx context.Context, serverID int64) (FactSnapshot, error)
}

type SQLRepository struct {
	conn *sql.DB
}

func NewSQLRepository(conn *sql.DB) SQLRepository {
	return SQLRepository{conn: conn}
}

func (r SQLRepository) Append(ctx context.Context, snapshot FactSnapshot) (FactSnapshot, error) {
	snapshot = normalizeFactSnapshot(snapshot)
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = time.Now().UTC()
	}

	result, err := r.conn.ExecContext(
		ctx,
		`INSERT INTO server_system_facts (
			server_id,
			hostname,
			os_name,
			os_version,
			kernel_version,
			architecture,
			cpu_model,
			cpu_cores,
			mem_total_kb,
			disk_total_kb,
			uptime_seconds,
			last_update_unix,
			collected_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ServerID,
		snapshot.Hostname,
		snapshot.OSName,
		snapshot.OSVersion,
		snapshot.KernelVersion,
		snapshot.Architecture,
		snapshot.CPUModel,
		snapshot.CPUCores,
		snapshot.MemTotalKB,
		snapshot.DiskTotalKB,
		snapshot.UptimeSeconds,
		snapshot.LastUpdateUnix,
		snapshot.CollectedAt,
	)
	if err != nil {
		return FactSnapshot{}, fmt.Errorf("system: append snapshot: %w", err)
	}

	snapshot.ID, err = result.LastInsertId()
	if err != nil {
		return FactSnapshot{}, fmt.Errorf("system: read snapshot last insert id: %w", err)
	}

	return snapshot, nil
}

func (r SQLRepository) HasAny(ctx context.Context, serverID int64) (bool, error) {
	var count int
	if err := r.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_system_facts WHERE server_id = ?`, serverID).Scan(&count); err != nil {
		return false, fmt.Errorf("system: check facts for server %d: %w", serverID, err)
	}
	return count > 0, nil
}

func (r SQLRepository) GetLatestByServer(ctx context.Context, serverID int64) (FactSnapshot, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT id, server_id, hostname, os_name, os_version, kernel_version, architecture, cpu_model, cpu_cores, mem_total_kb, disk_total_kb, uptime_seconds, last_update_unix, collected_at
		 FROM server_system_facts
		 WHERE server_id = ?
		 ORDER BY id DESC
		 LIMIT 1`,
		serverID,
	)

	var snapshot FactSnapshot
	var collectedAtRaw any
	if err := row.Scan(
		&snapshot.ID,
		&snapshot.ServerID,
		&snapshot.Hostname,
		&snapshot.OSName,
		&snapshot.OSVersion,
		&snapshot.KernelVersion,
		&snapshot.Architecture,
		&snapshot.CPUModel,
		&snapshot.CPUCores,
		&snapshot.MemTotalKB,
		&snapshot.DiskTotalKB,
		&snapshot.UptimeSeconds,
		&snapshot.LastUpdateUnix,
		&collectedAtRaw,
	); err != nil {
		if err == sql.ErrNoRows {
			return FactSnapshot{}, ErrNotFound
		}
		return FactSnapshot{}, fmt.Errorf("system: get latest snapshot for server %d: %w", serverID, err)
	}

	collectedAt, err := parseDatabaseTime(collectedAtRaw)
	if err != nil {
		return FactSnapshot{}, fmt.Errorf("system: parse collected_at: %w", err)
	}
	snapshot.CollectedAt = collectedAt
	return snapshot, nil
}

func normalizeFactSnapshot(snapshot FactSnapshot) FactSnapshot {
	snapshot.Hostname = strings.TrimSpace(snapshot.Hostname)
	snapshot.OSName = strings.TrimSpace(snapshot.OSName)
	snapshot.OSVersion = strings.TrimSpace(snapshot.OSVersion)
	snapshot.KernelVersion = strings.TrimSpace(snapshot.KernelVersion)
	snapshot.Architecture = strings.TrimSpace(snapshot.Architecture)
	snapshot.CPUModel = strings.TrimSpace(snapshot.CPUModel)
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
