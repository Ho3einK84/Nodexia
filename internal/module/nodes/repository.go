package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/db"
)

var ErrNotFound = errors.New("nodes: not found")

const (
	SweepSourceScheduler = "scheduler"
	SweepSourceManual    = "manual"
	SweepSourceAction    = "action"
)

const (
	manualHistoryCooldown = 5 * time.Minute
	uptimePruneInterval   = 6 * time.Hour
	uptimeStripDays       = 30
)

var (
	sweepSeq         atomic.Uint64
	historyPruneMu   sync.Mutex
	lastHistoryPrune = map[string]time.Time{}
)

// Snapshot is one discovered node instance. ServiceName carries the dynamic
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
	SweepID      string
	LastSeenAt   time.Time
	LatencyMS    int
	CollectedAt  time.Time
}

type Repository interface {
	HasAny(ctx context.Context, serverID int64) (bool, error)
	ReplaceLatest(ctx context.Context, serverID int64, snapshots []Snapshot, collectedAt time.Time) error
	ReplaceLatestWithSource(ctx context.Context, serverID int64, snapshots []Snapshot, collectedAt time.Time, source string) error
	GetLatestByServer(ctx context.Context, serverID int64) ([]Snapshot, error)
	// ListLatestNodeStatus aggregates the most recent discovery batch per server
	// into a fleet-wide node-status summary, for the home dashboard glance.
	ListLatestNodeStatus(ctx context.Context) ([]ServerNodeStatus, error)
	// UptimeStats aggregates the persisted per-sweep status observations for a
	// server since the given time, keyed by canonical UptimeKey(nodeType, serviceName).
	UptimeStats(ctx context.Context, serverID int64, since time.Time) (map[string]NodeUptime, error)
	// UptimeStatsAt aggregates uptime stats relative to an explicit reference
	// timestamp (used to build aligned 30-day UTC daily buckets deterministically).
	UptimeStatsAt(ctx context.Context, serverID int64, since, now time.Time) (map[string]NodeUptime, error)
}

// UptimeDay represents one UTC calendar day in the 30-day node uptime strip.
type UptimeDay struct {
	Date    string // YYYY-MM-DD (UTC)
	Status  string // "online" | "degraded" | "offline" | "empty"
	Checks  int
	Running int
	Percent int // -1 when Checks == 0
}

// NodeUptime summarises the recorded status observations and daily breakdown
// for one node over the 30-day window.
type NodeUptime struct {
	Checks  int         // total observations in the window
	Running int         // observations in a running state
	Percent int         // -1 when Checks == 0
	Days    []UptimeDay // 30 chronological UTC calendar days ending at now
}

// UptimeKey builds the canonical lowercase map key UptimeStats uses for one node.
func UptimeKey(nodeType, serviceName string) string {
	return strings.ToLower(strings.TrimSpace(nodeType)) + "|" + strings.ToLower(strings.TrimSpace(serviceName))
}

// IsRunningHealth reports whether a discovered health status represents a
// healthy active node process.
func IsRunningHealth(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "active", "healthy":
		return true
	default:
		return false
	}
}

// IsDegradedHealth reports whether a discovered health status is transitional
// or degraded (restarting, unhealthy healthcheck, activating).
func IsDegradedHealth(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "restarting", "degraded", "unhealthy", "activating", "starting":
		return true
	default:
		return false
	}
}

// isPlaceholderNodeType reports whether a snapshot row represents the sentinel
// "no nodes detected" placeholder rather than an actual node instance.
func isPlaceholderNodeType(nodeType string) bool {
	switch strings.ToLower(strings.TrimSpace(nodeType)) {
	case "", "none", "not_detected":
		return true
	default:
		return false
	}
}

// nodeStatusRetention is how long per-sweep status observations are kept.
// 60 days comfortably covers the 30-day uptime window plus slack.
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

type txBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type SQLRepository struct {
	db db.DBTX
}

func NewSQLRepository(conn db.DBTX) SQLRepository {
	return SQLRepository{db: conn}
}

func (r SQLRepository) HasAny(ctx context.Context, serverID int64) (bool, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_snapshots WHERE server_id = ?`, serverID).Scan(&count); err != nil {
		return false, fmt.Errorf("nodes: check snapshots for server %d: %w", serverID, err)
	}
	return count > 0, nil
}

func (r SQLRepository) ReplaceLatest(ctx context.Context, serverID int64, snapshots []Snapshot, collectedAt time.Time) error {
	return r.ReplaceLatestWithSource(ctx, serverID, snapshots, collectedAt, SweepSourceScheduler)
}

func (r SQLRepository) ReplaceLatestWithSource(ctx context.Context, serverID int64, snapshots []Snapshot, collectedAt time.Time, source string) error {
	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	} else {
		collectedAt = collectedAt.UTC()
	}

	source = normalizeSweepSource(source)
	sweepID := nextSweepID(collectedAt)

	if len(snapshots) == 0 {
		snapshots = []Snapshot{{
			ServerID:     serverID,
			NodeType:     "none",
			InstallMode:  "unknown",
			HealthStatus: "not_detected",
			Confidence:   "low",
			Evidence:     []string{"No node detector matched the collected evidence."},
			SweepID:      sweepID,
			CollectedAt:  collectedAt,
		}}
	}

	execer := r.db
	var tx *sql.Tx
	if beginner, ok := r.db.(txBeginner); ok {
		var err error
		tx, err = beginner.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("nodes: begin replace latest transaction: %w", err)
		}
		execer = tx
	}

	rollback := func(cause error) error {
		if tx != nil {
			_ = tx.Rollback()
		}
		return cause
	}

	priorLastSeen, err := loadPriorLastSeen(ctx, execer, serverID)
	if err != nil {
		return rollback(err)
	}

	// Atomically replace this server's latest snapshot generation so node_snapshots
	// never accumulates stale historical sweep batches or suffers datetime rounding
	// drift across SQLite/MySQL.
	if _, err := execer.ExecContext(ctx, `DELETE FROM node_snapshots WHERE server_id = ?`, serverID); err != nil {
		return rollback(fmt.Errorf("nodes: clear prior snapshots for %d: %w", serverID, err))
	}

	for _, snapshot := range snapshots {
		snapshot = normalizeSnapshot(snapshot)
		snapshot.ServerID = serverID
		snapshot.CollectedAt = collectedAt
		snapshot.SweepID = sweepID
		if snapshot.LatencyMS < 0 {
			snapshot.LatencyMS = 0
		}

		if !isPlaceholderNodeType(snapshot.NodeType) {
			if IsRunningHealth(snapshot.HealthStatus) {
				if snapshot.LastSeenAt.IsZero() {
					snapshot.LastSeenAt = collectedAt
				} else {
					snapshot.LastSeenAt = snapshot.LastSeenAt.UTC()
				}
			} else if snapshot.LastSeenAt.IsZero() {
				if prev, ok := priorLastSeen[UptimeKey(snapshot.NodeType, snapshot.ServiceName)]; ok && !prev.IsZero() {
					snapshot.LastSeenAt = prev
				}
			} else {
				snapshot.LastSeenAt = snapshot.LastSeenAt.UTC()
			}
		}

		dependenciesJSON, err := json.Marshal(nonNilSlice(snapshot.Dependencies))
		if err != nil {
			return rollback(fmt.Errorf("nodes: marshal dependencies: %w", err))
		}
		evidenceJSON, err := json.Marshal(nonNilSlice(snapshot.Evidence))
		if err != nil {
			return rollback(fmt.Errorf("nodes: marshal evidence: %w", err))
		}

		var lastSeenParam sql.NullTime
		if !snapshot.LastSeenAt.IsZero() {
			lastSeenParam = sql.NullTime{Time: snapshot.LastSeenAt.UTC(), Valid: true}
		}

		if _, err := execer.ExecContext(
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
				sweep_id,
				last_seen_at,
				latency_ms,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
			snapshot.SweepID,
			lastSeenParam,
			snapshot.LatencyMS,
			snapshot.CollectedAt,
		); err != nil {
			return rollback(fmt.Errorf("nodes: insert snapshot: %w", err))
		}
	}

	latestHistory, err := loadLatestHistoryByNode(ctx, execer, serverID, collectedAt)
	if err != nil {
		return rollback(err)
	}

	// Record one status observation per REAL node for the 30-day uptime history.
	// Manual/action refreshes within the cooldown window only emit a new history
	// row when health status actually changes, avoiding uptime skew while keeping
	// immediate transition visibility.
	for _, snapshot := range snapshots {
		snapshot = normalizeSnapshot(snapshot)
		if isPlaceholderNodeType(snapshot.NodeType) {
			continue
		}
		healthLower := strings.ToLower(snapshot.HealthStatus)
		if shouldSkipDuplicateObservation(source, latestHistory[UptimeKey(snapshot.NodeType, snapshot.ServiceName)], healthLower, collectedAt) {
			continue
		}
		if _, err := execer.ExecContext(ctx,
			`INSERT INTO node_status_history (server_id, node_type, service_name, health_status, observed_at, source)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			serverID,
			snapshot.NodeType,
			snapshot.ServiceName,
			healthLower,
			collectedAt,
			source,
		); err != nil {
			return rollback(fmt.Errorf("nodes: insert status observation: %w", err))
		}
	}

	if shouldPruneStatusHistory(r.db, serverID, collectedAt, source) {
		if _, err := execer.ExecContext(ctx,
			`DELETE FROM node_status_history WHERE server_id = ? AND observed_at < ?`,
			serverID,
			collectedAt.Add(-nodeStatusRetention),
		); err != nil {
			return rollback(fmt.Errorf("nodes: trim status history: %w", err))
		}
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("nodes: commit replace latest transaction: %w", err)
		}
	}

	return nil
}

type priorHistoryObs struct {
	health     string
	observedAt time.Time
}

func loadPriorLastSeen(ctx context.Context, q db.DBTX, serverID int64) (map[string]time.Time, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT node_type, service_name, health_status, last_seen_at, created_at
		 FROM node_snapshots
		 WHERE server_id = ?`,
		serverID,
	)
	if err != nil {
		return nil, fmt.Errorf("nodes: load prior last_seen for server %d: %w", serverID, err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]time.Time{}
	for rows.Next() {
		var nodeType, serviceName, health string
		var lastSeenRaw, createdAtRaw any
		if err := rows.Scan(&nodeType, &serviceName, &health, &lastSeenRaw, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("nodes: scan prior last_seen: %w", err)
		}
		if isPlaceholderNodeType(nodeType) {
			continue
		}
		key := UptimeKey(nodeType, serviceName)
		lastSeen, _ := parseDatabaseTime(lastSeenRaw)
		if lastSeen.IsZero() && IsRunningHealth(health) {
			lastSeen, _ = parseDatabaseTime(createdAtRaw)
		}
		if !lastSeen.IsZero() && lastSeen.After(out[key]) {
			out[key] = lastSeen
		}
	}
	return out, rows.Err()
}

func loadLatestHistoryByNode(ctx context.Context, q db.DBTX, serverID int64, refTime time.Time) (map[string]priorHistoryObs, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT node_type, service_name, health_status, observed_at
		 FROM node_status_history
		 WHERE server_id = ? AND observed_at >= ?
		 ORDER BY id DESC`,
		serverID,
		refTime.UTC().Add(-24*time.Hour),
	)
	if err != nil {
		return nil, fmt.Errorf("nodes: load recent status history for server %d: %w", serverID, err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]priorHistoryObs{}
	for rows.Next() {
		var nodeType, serviceName, health string
		var observedRaw any
		if err := rows.Scan(&nodeType, &serviceName, &health, &observedRaw); err != nil {
			return nil, fmt.Errorf("nodes: scan recent history row: %w", err)
		}
		key := UptimeKey(nodeType, serviceName)
		if _, seen := out[key]; seen {
			continue
		}
		observedAt, err := parseDatabaseTime(observedRaw)
		if err != nil {
			continue
		}
		out[key] = priorHistoryObs{
			health:     strings.ToLower(strings.TrimSpace(health)),
			observedAt: observedAt,
		}
	}
	return out, rows.Err()
}

func shouldSkipDuplicateObservation(source string, prior priorHistoryObs, newHealth string, observedAt time.Time) bool {
	if source == SweepSourceScheduler || prior.observedAt.IsZero() {
		return false
	}
	if prior.health != strings.ToLower(strings.TrimSpace(newHealth)) {
		return false
	}
	delta := observedAt.Sub(prior.observedAt)
	return delta >= 0 && delta < manualHistoryCooldown
}

func shouldPruneStatusHistory(conn db.DBTX, serverID int64, now time.Time, source string) bool {
	if source != SweepSourceScheduler {
		return false
	}
	key := fmt.Sprintf("%p:%d", conn, serverID)
	historyPruneMu.Lock()
	defer historyPruneMu.Unlock()
	last := lastHistoryPrune[key]
	if !last.IsZero() && now.Sub(last) >= 0 && now.Sub(last) < uptimePruneInterval {
		return false
	}
	lastHistoryPrune[key] = now
	return true
}

// UptimeStats aggregates the recorded observations for one server since the
// given time, keyed by UptimeKey(nodeType, serviceName).
func (r SQLRepository) UptimeStats(ctx context.Context, serverID int64, since time.Time) (map[string]NodeUptime, error) {
	return r.UptimeStatsAt(ctx, serverID, since, time.Now().UTC())
}

// UptimeStatsAt computes uptime totals and a 30-day aligned UTC daily breakdown
// ending on now.UTC().
func (r SQLRepository) UptimeStatsAt(ctx context.Context, serverID int64, since, now time.Time) (map[string]NodeUptime, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	since = since.UTC()

	rows, err := r.db.QueryContext(ctx,
		`SELECT node_type, service_name, health_status, observed_at
		 FROM node_status_history
		 WHERE server_id = ? AND observed_at >= ?
		 ORDER BY observed_at ASC`,
		serverID,
		since,
	)
	if err != nil {
		return nil, fmt.Errorf("nodes: uptime stats for server %d: %w", serverID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	type dayAccum struct {
		checks  int
		running int
	}
	perNodeDay := map[string]map[string]*dayAccum{}
	totals := map[string]*NodeUptime{}

	for rows.Next() {
		var nodeType, serviceName, health string
		var observedRaw any
		if err := rows.Scan(&nodeType, &serviceName, &health, &observedRaw); err != nil {
			return nil, fmt.Errorf("nodes: scan uptime row: %w", err)
		}
		observedAt, err := parseDatabaseTime(observedRaw)
		if err != nil {
			return nil, fmt.Errorf("nodes: parse uptime observed_at: %w", err)
		}

		key := UptimeKey(nodeType, serviceName)
		stat := totals[key]
		if stat == nil {
			stat = &NodeUptime{Percent: -1}
			totals[key] = stat
			perNodeDay[key] = map[string]*dayAccum{}
		}

		stat.Checks++
		dayKey := observedAt.UTC().Format("2006-01-02")
		dayAcc := perNodeDay[key][dayKey]
		if dayAcc == nil {
			dayAcc = &dayAccum{}
			perNodeDay[key][dayKey] = dayAcc
		}
		dayAcc.checks++

		if IsRunningHealth(health) {
			stat.Running++
			dayAcc.running++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	todayUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startUTC := todayUTC.AddDate(0, 0, -(uptimeStripDays - 1))

	out := make(map[string]NodeUptime, len(totals))
	for key, stat := range totals {
		if stat.Checks > 0 {
			stat.Percent = int(math.Round(float64(stat.Running) * 100 / float64(stat.Checks)))
		}
		daysMap := perNodeDay[key]
		days := make([]UptimeDay, 0, uptimeStripDays)
		for offset := 0; offset < uptimeStripDays; offset++ {
			dateStr := startUTC.AddDate(0, 0, offset).Format("2006-01-02")
			acc := daysMap[dateStr]
			if acc == nil || acc.checks == 0 {
				days = append(days, UptimeDay{
					Date:    dateStr,
					Status:  "empty",
					Percent: -1,
				})
				continue
			}
			pct := int(math.Round(float64(acc.running) * 100 / float64(acc.checks)))
			status := "degraded"
			switch acc.running {
			case acc.checks:
				status = "online"
			case 0:
				status = "offline"
			}
			days = append(days, UptimeDay{
				Date:    dateStr,
				Status:  status,
				Checks:  acc.checks,
				Running: acc.running,
				Percent: pct,
			})
		}
		stat.Days = days
		out[key] = *stat
	}
	return out, nil
}

func (r SQLRepository) GetLatestByServer(ctx context.Context, serverID int64) ([]Snapshot, error) {
	var sweepID string
	var latestRaw any
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT sweep_id, created_at
		 FROM node_snapshots
		 WHERE server_id = ?
		 ORDER BY id DESC
		 LIMIT 1`,
		serverID,
	).Scan(&sweepID, &latestRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("nodes: get latest sweep for server %d: %w", serverID, err)
	}

	var (
		rows *sql.Rows
		err  error
	)
	const selectCols = `SELECT id, server_id, node_type, service_name, version, health_status, active_ports, xray_ports, dependencies_json, install_mode, service_port, api_port, protocol, data_dir, confidence, evidence_json, sweep_id, last_seen_at, latency_ms, created_at
		 FROM node_snapshots`

	if strings.TrimSpace(sweepID) != "" {
		rows, err = r.db.QueryContext(
			ctx,
			selectCols+` WHERE server_id = ? AND sweep_id = ? ORDER BY id ASC`,
			serverID,
			sweepID,
		)
	} else {
		latestCreatedAt, parseErr := parseDatabaseTime(latestRaw)
		if parseErr != nil {
			return nil, fmt.Errorf("nodes: parse latest created_at for server %d: %w", serverID, parseErr)
		}
		rows, err = r.db.QueryContext(
			ctx,
			selectCols+` WHERE server_id = ? AND created_at = ? ORDER BY id ASC`,
			serverID,
			latestCreatedAt,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("nodes: list latest snapshots for server %d: %w", serverID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

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

// ListLatestNodeStatus returns one summary per server containing real discovered
// nodes from its latest batch. Supports both sweep_id generation matching and
// legacy created_at matching for un-swept pre-migration rows.
func (r SQLRepository) ListLatestNodeStatus(ctx context.Context) ([]ServerNodeStatus, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT ns.server_id, s.name, ns.node_type, ns.health_status
		 FROM node_snapshots ns
		 JOIN servers s ON s.id = ns.server_id
		 WHERE (
		   ns.sweep_id <> '' AND ns.sweep_id = (
		     SELECT ns2.sweep_id FROM node_snapshots ns2
		     WHERE ns2.server_id = ns.server_id
		     ORDER BY ns2.id DESC LIMIT 1
		   )
		 ) OR (
		   ns.sweep_id = '' AND ns.created_at = (
		     SELECT ns2.created_at FROM node_snapshots ns2
		     WHERE ns2.server_id = ns.server_id
		     ORDER BY ns2.id DESC LIMIT 1
		   )
		 )
		 ORDER BY s.name ASC, ns.id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("nodes: list latest node status: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

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
		if isPlaceholderNodeType(nodeType) {
			continue
		}
		st.Total++
		switch {
		case IsRunningHealth(health):
			st.Running++
		case strings.EqualFold(strings.TrimSpace(health), "stopped"):
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
	var lastSeenRaw any
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
		&snapshot.SweepID,
		&lastSeenRaw,
		&snapshot.LatencyMS,
		&createdAtRaw,
	); err != nil {
		return Snapshot{}, err
	}

	snapshot.ActivePorts = splitCSV(activePorts)
	snapshot.XrayPorts = splitCSV(xrayPorts)

	if trimmed := strings.TrimSpace(dependenciesJSON); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &snapshot.Dependencies); err != nil {
			return Snapshot{}, fmt.Errorf("unmarshal dependencies: %w", err)
		}
	}
	if trimmed := strings.TrimSpace(evidenceJSON); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &snapshot.Evidence); err != nil {
			return Snapshot{}, fmt.Errorf("unmarshal evidence: %w", err)
		}
	}

	lastSeenAt, err := parseDatabaseTime(lastSeenRaw)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse last_seen_at: %w", err)
	}
	snapshot.LastSeenAt = lastSeenAt

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
	snapshot.SweepID = strings.TrimSpace(snapshot.SweepID)
	snapshot.ActivePorts = normalizeStringSlice(snapshot.ActivePorts)
	snapshot.XrayPorts = normalizeStringSlice(snapshot.XrayPorts)
	snapshot.Dependencies = normalizeStringSlice(snapshot.Dependencies)
	snapshot.Evidence = normalizeStringSlice(snapshot.Evidence)
	return snapshot
}

func normalizeSweepSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case SweepSourceManual:
		return SweepSourceManual
	case SweepSourceAction:
		return SweepSourceAction
	default:
		return SweepSourceScheduler
	}
}

func nextSweepID(ts time.Time) string {
	return strconv.FormatInt(ts.UTC().UnixNano(), 10) + "-" + strconv.FormatUint(sweepSeq.Add(1), 10)
}

func nonNilSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
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
		if typed.IsZero() {
			return time.Time{}, nil
		}
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
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
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
