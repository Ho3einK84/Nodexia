package monitoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type TrafficRow struct {
	Label      string `json:"label"`
	RXBytes    int64  `json:"rx_bytes"`
	TXBytes    int64  `json:"tx_bytes"`
	TotalBytes int64  `json:"total_bytes"`
}

type TrafficSnapshot struct {
	ID                  int64
	ServerID            int64
	Available           bool
	InterfaceName       string
	AvailableInterfaces []string
	DailyRows           []TrafficRow
	MonthlyRows         []TrafficRow
	PeakMbps            float64
	AvgMbps             float64
	Message             string
	CollectedAt         time.Time
}

type TrafficRepository interface {
	AppendTraffic(ctx context.Context, snapshot TrafficSnapshot) (TrafficSnapshot, error)
	GetLatestTrafficByServer(ctx context.Context, serverID int64) (TrafficSnapshot, error)
}

func (r SQLRepository) AppendTraffic(ctx context.Context, snapshot TrafficSnapshot) (TrafficSnapshot, error) {
	snapshot = normalizeTrafficSnapshot(snapshot)
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = time.Now().UTC()
	}

	availableInterfacesJSON, err := json.Marshal(snapshot.AvailableInterfaces)
	if err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: marshal vnstat interfaces: %w", err)
	}
	dailyRowsJSON, err := json.Marshal(snapshot.DailyRows)
	if err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: marshal vnstat daily rows: %w", err)
	}
	monthlyRowsJSON, err := json.Marshal(snapshot.MonthlyRows)
	if err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: marshal vnstat monthly rows: %w", err)
	}

	available := 0
	if snapshot.Available {
		available = 1
	}

	result, err := r.conn.ExecContext(
		ctx,
		`INSERT INTO vnstat_snapshots (
			server_id,
			available,
			interface_name,
			available_interfaces_json,
			daily_rows_json,
			monthly_rows_json,
			peak_mbps,
			avg_mbps,
			message,
			collected_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ServerID,
		available,
		snapshot.InterfaceName,
		string(availableInterfacesJSON),
		string(dailyRowsJSON),
		string(monthlyRowsJSON),
		snapshot.PeakMbps,
		snapshot.AvgMbps,
		snapshot.Message,
		snapshot.CollectedAt,
	)
	if err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: append vnstat snapshot: %w", err)
	}

	snapshot.ID, err = result.LastInsertId()
	if err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: read vnstat snapshot last insert id: %w", err)
	}

	return snapshot, nil
}

func (r SQLRepository) GetLatestTrafficByServer(ctx context.Context, serverID int64) (TrafficSnapshot, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT id, server_id, available, interface_name, available_interfaces_json,
		        daily_rows_json, monthly_rows_json, peak_mbps, avg_mbps, message, collected_at
		 FROM vnstat_snapshots
		 WHERE server_id = ?
		 ORDER BY id DESC
		 LIMIT 1`,
		serverID,
	)

	var snapshot TrafficSnapshot
	var available int
	var availableInterfacesJSON string
	var dailyRowsJSON string
	var monthlyRowsJSON string
	var collectedAtRaw any

	if err := row.Scan(
		&snapshot.ID,
		&snapshot.ServerID,
		&available,
		&snapshot.InterfaceName,
		&availableInterfacesJSON,
		&dailyRowsJSON,
		&monthlyRowsJSON,
		&snapshot.PeakMbps,
		&snapshot.AvgMbps,
		&snapshot.Message,
		&collectedAtRaw,
	); err != nil {
		if err == sql.ErrNoRows {
			return TrafficSnapshot{}, ErrNotFound
		}
		return TrafficSnapshot{}, fmt.Errorf("monitoring: get latest vnstat snapshot for server %d: %w", serverID, err)
	}

	snapshot.Available = available == 1
	if err := json.Unmarshal([]byte(availableInterfacesJSON), &snapshot.AvailableInterfaces); err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: unmarshal vnstat interfaces: %w", err)
	}
	if err := json.Unmarshal([]byte(dailyRowsJSON), &snapshot.DailyRows); err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: unmarshal vnstat daily rows: %w", err)
	}
	if err := json.Unmarshal([]byte(monthlyRowsJSON), &snapshot.MonthlyRows); err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: unmarshal vnstat monthly rows: %w", err)
	}

	collectedAt, err := parseDatabaseTime(collectedAtRaw)
	if err != nil {
		return TrafficSnapshot{}, fmt.Errorf("monitoring: parse vnstat collected_at: %w", err)
	}
	snapshot.CollectedAt = collectedAt
	return normalizeTrafficSnapshot(snapshot), nil
}

func normalizeTrafficSnapshot(snapshot TrafficSnapshot) TrafficSnapshot {
	snapshot.InterfaceName = strings.TrimSpace(snapshot.InterfaceName)
	snapshot.Message = strings.TrimSpace(snapshot.Message)
	snapshot.AvailableInterfaces = normalizeInterfaces(snapshot.AvailableInterfaces)
	snapshot.DailyRows = normalizeTrafficRows(snapshot.DailyRows)
	snapshot.MonthlyRows = normalizeTrafficRows(snapshot.MonthlyRows)
	return snapshot
}

func normalizeInterfaces(values []string) []string {
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

func normalizeTrafficRows(rows []TrafficRow) []TrafficRow {
	out := make([]TrafficRow, 0, len(rows))
	for _, row := range rows {
		row.Label = strings.TrimSpace(row.Label)
		if row.TotalBytes == 0 {
			row.TotalBytes = row.RXBytes + row.TXBytes
		}
		out = append(out, row)
	}
	return out
}
