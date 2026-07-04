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

// Snapshot is one discovered node instance.  ServiceName carries the dynamic
// node name (e.g. "node", "node2", "rebecca-node") read from the remote
// configuration during discovery.
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
	DataDir      string
	Confidence   string
	Dependencies []string
	Evidence     []string
	CollectedAt  time.Time
}

type Repository interface {
	HasAny(ctx context.Context, serverID int64) (bool, error)
	ReplaceLatest(ctx context.Context, serverID int64, snapshots []Snapshot, collectedAt time.Time) error
	GetLatestByServer(ctx context.Context, serverID int64) ([]Snapshot, error)
	// ListLatestNodeStatus aggregates the most recent discovery batch per server
	// into a fleet-wide node-status summary, for the home dashboard glance.
	ListLatestNodeStatus(ctx context.Context) ([]ServerNodeStatus, error)
	// UptimeStats aggregates the persisted per-sweep status observations for a
	// server since the given time, keyed by nodeType+"|"+serviceName. Callers use
	// it to render an uptime percentage per node card.
	UptimeStats(ctx context.Context, serverID int64, since time.Time) (map[string]NodeUptime, error)
}

// NodeUptime summarises the recorded status observations for one node.
type NodeUptime struct {
	Checks  int // total observations in the window
	Running int // observations in the "running" state
}

// UptimeKey builds the map key UptimeStats uses for one node.
func UptimeKey(nodeType, serviceName string) string {
	return strings.TrimSpace(nodeType) + "|" + strings.TrimSpace(serviceName)
}

// nodeStatusRetention is how long per-sweep status observations are kept.
// 60 days comfortably covers the 30-day uptime window plus slack; older rows
// are trimmed on write so no scheduled cleanup is needed.
const nodeStatusRetention = 60 * 24 * time.Hour

// ServerNodeStatus summarises one server's latest node-discovery batch: how many
// real nodes were found and how many are running/stopped/other. Placeholder
// snapshots (node_type "none"/"not_detected") are excluded from the counts, so a
// server with no detected node reports Total 0.
type ServerNodeStatus struct {
	ServerID   int64
	ServerName string
	Total      int
	Running    int
	Stopped    int
	Other      int
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
		// One sweep = one created_at. GetLatestByServer reloads "the latest
		// batch" by a single created_at value, so every row written here must
		// share the timestamp passed in — otherwise nodes discovered by
		// different providers (PasarGuard vs Rebecca) split across timestamps
		// and only one family is ever returned.
		snapshot.CollectedAt = collectedAt

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
				data_dir,
				confidence,
				evidence_json,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
			snapshot.DataDir,
			snapshot.Confidence,
			string(evidenceJSON),
			snapshot.CollectedAt,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("nodes: insert snapshot: %w", err)
		}
	}

	// Record one status observation per REAL node for the uptime history.
	// Placeholder "no node detected" rows are not observations of a node.
	for _, snapshot := range snapshots {
		snapshot = normalizeSnapshot(snapshot)
		switch snapshot.NodeType {
		case "", "none", "not_detected":
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO node_status_history (server_id, node_type, service_name, health_status, observed_at)
			 VALUES (?, ?, ?, ?, ?)`,
			serverID,
			snapshot.NodeType,
			snapshot.ServiceName,
			strings.ToLower(snapshot.HealthStatus),
			collectedAt,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("nodes: insert status observation: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM node_status_history WHERE server_id = ? AND observed_at < ?`,
		serverID,
		collectedAt.Add(-nodeStatusRetention),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("nodes: trim status history: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("nodes: commit replace latest transaction: %w", err)
	}

	return nil
}

// UptimeStats aggregates the recorded observations for one server since the
// given time, keyed by UptimeKey(nodeType, serviceName).
func (r SQLRepository) UptimeStats(ctx context.Context, serverID int64, since time.Time) (map[string]NodeUptime, error) {
	rows, err := r.conn.QueryContext(ctx,
		`SELECT node_type, service_name, health_status
		 FROM node_status_history
		 WHERE server_id = ? AND observed_at >= ?`,
		serverID,
		since.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("nodes: uptime stats for server %d: %w", serverID, err)
	}
	defer rows.Close()

	out := map[string]NodeUptime{}
	for rows.Next() {
		var nodeType, serviceName, health string
		if err := rows.Scan(&nodeType, &serviceName, &health); err != nil {
			return nil, fmt.Errorf("nodes: scan uptime row: %w", err)
		}
		key := UptimeKey(nodeType, serviceName)
		stat := out[key]
		stat.Checks++
		if strings.EqualFold(strings.TrimSpace(health), "running") {
			stat.Running++
		}
		out[key] = stat
	}
	return out, rows.Err()
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
		`SELECT id, server_id, node_type, service_name, version, health_status, active_ports, xray_ports, dependencies_json, install_mode, service_port, api_port, protocol, data_dir, confidence, evidence_json, created_at
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

// ListLatestNodeStatus returns one row per discovered node in each server's most
// recent batch, joined to the server name, and folds them into per-server
// summaries in Go. The latest batch is the rows sharing the newest created_at for
// that server (one sweep = one created_at), matched with the same correlated
// "ORDER BY id DESC LIMIT 1" rule GetLatestByServer uses. Placeholder rows
// (node_type "none") are filtered out of the counts.
func (r SQLRepository) ListLatestNodeStatus(ctx context.Context) ([]ServerNodeStatus, error) {
	rows, err := r.conn.QueryContext(ctx,
		`SELECT ns.server_id, s.name, ns.node_type, ns.health_status
		 FROM node_snapshots ns
		 JOIN servers s ON s.id = ns.server_id
		 WHERE ns.created_at = (
		   SELECT created_at FROM node_snapshots ns2
		   WHERE ns2.server_id = ns.server_id
		   ORDER BY id DESC LIMIT 1
		 )
		 ORDER BY s.name ASC, ns.id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("nodes: list latest node status: %w", err)
	}
	defer rows.Close()

	byServer := map[int64]*ServerNodeStatus{}
	order := make([]int64, 0)
	for rows.Next() {
		var serverID int64
		var name, nodeType, health string
		if err := rows.Scan(&serverID, &name, &nodeType, &health); err != nil {
			return nil, fmt.Errorf("nodes: scan node status: %w", err)
		}
		st, ok := byServer[serverID]
		if !ok {
			st = &ServerNodeStatus{ServerID: serverID, ServerName: name}
			byServer[serverID] = st
			order = append(order, serverID)
		}
		// Skip placeholder "no node detected" rows: they aren't real nodes.
		switch strings.TrimSpace(nodeType) {
		case "", "none", "not_detected":
			continue
		}
		st.Total++
		switch strings.ToLower(strings.TrimSpace(health)) {
		case "running":
			st.Running++
		case "stopped":
			st.Stopped++
		default:
			st.Other++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nodes: iterate node status: %w", err)
	}

	out := make([]ServerNodeStatus, 0, len(order))
	for _, id := range order {
		out = append(out, *byServer[id])
	}
	return out, nil
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
		&snapshot.DataDir,
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
	snapshot.DataDir = strings.TrimSpace(snapshot.DataDir)
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
