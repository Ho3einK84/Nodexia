package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("nodes: not found")

type Snapshot struct {
	ID           int64
	ServerID     int64
	NodeType     string
	ServiceName  string
	InstallMode  string
	Version      string
	HealthStatus string
	ActivePorts  []string
	XrayPorts    []string
	ServicePort  string
	APIPort      string
	Protocol     string
	Confidence   string
	Dependencies []string
	Evidence     []string
	CollectedAt  time.Time
}

type Repository interface {
	HasAny(ctx context.Context, serverID int64) (bool, error)
	ReplaceLatest(ctx context.Context, serverID int64, snapshots []Snapshot, collectedAt time.Time) error
	GetLatestByServer(ctx context.Context, serverID int64) ([]Snapshot, error)
}

type SQLRepository struct {
	conn *sql.DB
}

func NewSQLRepository(conn *sql.DB) SQLRepository {
	return SQLRepository{conn: conn}
}

func (r SQLRepository) HasAny(ctx context.Context, serverID int64) (bool, error) {
	var count int
	if err := r.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_snapshots WHERE server_id = ?`, serverID).Scan(&count); err != nil {
		return false, fmt.Errorf("nodes: check snapshots for server %d: %w", serverID, err)
	}
	return count > 0, nil
}

func (r SQLRepository) ReplaceLatest(ctx context.Context, serverID int64, snapshots []Snapshot, collectedAt time.Time) error {
	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	if len(snapshots) == 0 {
		snapshots = []Snapshot{{
			ServerID:     serverID,
			NodeType:     "none",
			InstallMode:  "unknown",
			HealthStatus: "not_detected",
			Confidence:   "low",
			Evidence:     []string{"No node detector matched the collected evidence."},
			CollectedAt:  collectedAt,
		}}
	}

	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("nodes: begin replace latest transaction: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM node_snapshots WHERE server_id = ? AND created_at = ?`, serverID, collectedAt); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("nodes: clear duplicate snapshots for %d: %w", serverID, err)
	}

	for _, snapshot := range snapshots {
		snapshot = normalizeSnapshot(snapshot)
		snapshot.ServerID = serverID
		if snapshot.CollectedAt.IsZero() {
			snapshot.CollectedAt = collectedAt
		}

		dependenciesJSON, err := json.Marshal(snapshot.Dependencies)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("nodes: marshal dependencies: %w", err)
		}
		evidenceJSON, err := json.Marshal(snapshot.Evidence)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("nodes: marshal evidence: %w", err)
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO node_snapshots (
				server_id,
				node_type,
				service_name,
				version,
				health_status,
				active_ports,
				xray_ports,
				dependencies_json,
				install_mode,
				service_port,
				api_port,
				protocol,
				confidence,
				evidence_json,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshot.ServerID,
			snapshot.NodeType,
			snapshot.ServiceName,
			snapshot.Version,
			snapshot.HealthStatus,
			strings.Join(snapshot.ActivePorts, ", "),
			strings.Join(snapshot.XrayPorts, ", "),
			string(dependenciesJSON),
			snapshot.InstallMode,
			snapshot.ServicePort,
			snapshot.APIPort,
			snapshot.Protocol,
			snapshot.Confidence,
			string(evidenceJSON),
			snapshot.CollectedAt,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("nodes: insert snapshot: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("nodes: commit replace latest transaction: %w", err)
	}

	return nil
}

func (r SQLRepository) GetLatestByServer(ctx context.Context, serverID int64) ([]Snapshot, error) {
	var latestRaw any
	if err := r.conn.QueryRowContext(
		ctx,
		`SELECT created_at
		 FROM node_snapshots
		 WHERE server_id = ?
		 ORDER BY id DESC
		 LIMIT 1`,
		serverID,
	).Scan(&latestRaw); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("nodes: get latest created_at for server %d: %w", serverID, err)
	}

	latestCreatedAt, err := parseDatabaseTime(latestRaw)
	if err != nil {
		return nil, fmt.Errorf("nodes: parse latest created_at for server %d: %w", serverID, err)
	}

	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT id, server_id, node_type, service_name, version, health_status, active_ports, xray_ports, dependencies_json, install_mode, service_port, api_port, protocol, confidence, evidence_json, created_at
		 FROM node_snapshots
		 WHERE server_id = ? AND created_at = ?
		 ORDER BY id ASC`,
		serverID,
		latestCreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("nodes: list latest snapshots for server %d: %w", serverID, err)
	}
	defer rows.Close()

	snapshots := make([]Snapshot, 0)
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("nodes: scan snapshot row: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nodes: iterate snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		return nil, ErrNotFound
	}
	return snapshots, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSnapshot(scanner rowScanner) (Snapshot, error) {
	var snapshot Snapshot
	var activePorts string
	var xrayPorts string
	var dependenciesJSON string
	var evidenceJSON string
	var createdAtRaw any

	if err := scanner.Scan(
		&snapshot.ID,
		&snapshot.ServerID,
		&snapshot.NodeType,
		&snapshot.ServiceName,
		&snapshot.Version,
		&snapshot.HealthStatus,
		&activePorts,
		&xrayPorts,
		&dependenciesJSON,
		&snapshot.InstallMode,
		&snapshot.ServicePort,
		&snapshot.APIPort,
		&snapshot.Protocol,
		&snapshot.Confidence,
		&evidenceJSON,
		&createdAtRaw,
	); err != nil {
		return Snapshot{}, err
	}

	snapshot.ActivePorts = splitCSV(activePorts)
	snapshot.XrayPorts = splitCSV(xrayPorts)
	if err := json.Unmarshal([]byte(strings.TrimSpace(dependenciesJSON)), &snapshot.Dependencies); err != nil {
		return Snapshot{}, fmt.Errorf("unmarshal dependencies: %w", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(evidenceJSON)), &snapshot.Evidence); err != nil {
		return Snapshot{}, fmt.Errorf("unmarshal evidence: %w", err)
	}
	createdAt, err := parseDatabaseTime(createdAtRaw)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse created_at: %w", err)
	}
	snapshot.CollectedAt = createdAt
	return normalizeSnapshot(snapshot), nil
}

func normalizeSnapshot(snapshot Snapshot) Snapshot {
	snapshot.NodeType = strings.TrimSpace(snapshot.NodeType)
	snapshot.ServiceName = strings.TrimSpace(snapshot.ServiceName)
	snapshot.InstallMode = strings.TrimSpace(snapshot.InstallMode)
	snapshot.Version = strings.TrimSpace(snapshot.Version)
	snapshot.HealthStatus = strings.TrimSpace(snapshot.HealthStatus)
	snapshot.ServicePort = strings.TrimSpace(snapshot.ServicePort)
	snapshot.APIPort = strings.TrimSpace(snapshot.APIPort)
	snapshot.Protocol = strings.TrimSpace(snapshot.Protocol)
	snapshot.Confidence = strings.TrimSpace(snapshot.Confidence)
	snapshot.ActivePorts = normalizeStringSlice(snapshot.ActivePorts)
	snapshot.XrayPorts = normalizeStringSlice(snapshot.XrayPorts)
	snapshot.Dependencies = normalizeStringSlice(snapshot.Dependencies)
	snapshot.Evidence = normalizeStringSlice(snapshot.Evidence)
	return snapshot
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func splitCSV(value string) []string {
	return normalizeStringSlice(strings.Split(value, ","))
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
