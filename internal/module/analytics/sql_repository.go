package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SQLRepository struct {
	conn *sql.DB
}

func NewSQLRepository(conn *sql.DB) SQLRepository {
	return SQLRepository{conn: conn}
}

func (r SQLRepository) ListRawSince(ctx context.Context, serverID int64, since time.Time) ([]RawPoint, error) {
	rows, err := r.conn.QueryContext(ctx,
		`SELECT cpu_usage, ram_usage, swap_usage, disk_usage,
		        load_average_1, load_average_5, load_average_15, created_at
		 FROM system_snapshots
		 WHERE server_id = ? AND created_at >= ?
		 ORDER BY created_at ASC`,
		serverID, since,
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: list raw since: %w", err)
	}
	defer rows.Close()

	var points []RawPoint
	for rows.Next() {
		var p RawPoint
		var raw any
		if err := rows.Scan(&p.CPUUsage, &p.RAMUsage, &p.SwapUsage, &p.DiskUsage,
			&p.LoadAvg1, &p.LoadAvg5, &p.LoadAvg15, &raw); err != nil {
			return nil, fmt.Errorf("analytics: scan raw point: %w", err)
		}
		t, err := parseDatabaseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("analytics: parse raw point time: %w", err)
		}
		p.RecordedAt = t
		points = append(points, p)
	}
	return points, rows.Err()
}

func (r SQLRepository) ListHourlyRollups(ctx context.Context, serverID int64, since time.Time) ([]HourlyRollup, error) {
	rows, err := r.conn.QueryContext(ctx,
		`SELECT id, server_id, period_start, avg_cpu, avg_ram, avg_disk, avg_swap,
		        avg_load1, avg_load5, avg_load15, sample_count
		 FROM metric_rollups_hourly
		 WHERE server_id = ? AND period_start >= ?
		 ORDER BY period_start ASC`,
		serverID, since,
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: list hourly rollups: %w", err)
	}
	defer rows.Close()

	var rollups []HourlyRollup
	for rows.Next() {
		var rp HourlyRollup
		var raw any
		if err := rows.Scan(&rp.ID, &rp.ServerID, &raw,
			&rp.AvgCPU, &rp.AvgRAM, &rp.AvgDisk, &rp.AvgSwap,
			&rp.AvgLoad1, &rp.AvgLoad5, &rp.AvgLoad15, &rp.SampleCount); err != nil {
			return nil, fmt.Errorf("analytics: scan hourly rollup: %w", err)
		}
		t, err := parseDatabaseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("analytics: parse hourly period_start: %w", err)
		}
		rp.PeriodStart = t
		rollups = append(rollups, rp)
	}
	return rollups, rows.Err()
}

func (r SQLRepository) ListDailyRollups(ctx context.Context, serverID int64, since time.Time) ([]DailyRollup, error) {
	rows, err := r.conn.QueryContext(ctx,
		`SELECT id, server_id, period_start, avg_cpu, avg_ram, avg_disk, avg_swap,
		        avg_load1, avg_load5, avg_load15, sample_count
		 FROM metric_rollups_daily
		 WHERE server_id = ? AND period_start >= ?
		 ORDER BY period_start ASC`,
		serverID, since,
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: list daily rollups: %w", err)
	}
	defer rows.Close()

	var rollups []DailyRollup
	for rows.Next() {
		var rp DailyRollup
		var raw any
		if err := rows.Scan(&rp.ID, &rp.ServerID, &raw,
			&rp.AvgCPU, &rp.AvgRAM, &rp.AvgDisk, &rp.AvgSwap,
			&rp.AvgLoad1, &rp.AvgLoad5, &rp.AvgLoad15, &rp.SampleCount); err != nil {
			return nil, fmt.Errorf("analytics: scan daily rollup: %w", err)
		}
		t, err := parseDatabaseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("analytics: parse daily period_start: %w", err)
		}
		rp.PeriodStart = t
		rollups = append(rollups, rp)
	}
	return rollups, rows.Err()
}

func (r SQLRepository) HasHourlyRollup(ctx context.Context, serverID int64, periodStart time.Time) (bool, error) {
	var count int
	err := r.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM metric_rollups_hourly WHERE server_id = ? AND period_start = ?`,
		serverID, periodStart,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("analytics: check hourly rollup: %w", err)
	}
	return count > 0, nil
}

func (r SQLRepository) HasDailyRollup(ctx context.Context, serverID int64, periodStart time.Time) (bool, error) {
	var count int
	err := r.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM metric_rollups_daily WHERE server_id = ? AND period_start = ?`,
		serverID, periodStart,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("analytics: check daily rollup: %w", err)
	}
	return count > 0, nil
}

func (r SQLRepository) InsertHourlyRollup(ctx context.Context, serverID int64, rp HourlyRollup) error {
	_, err := r.conn.ExecContext(ctx,
		`INSERT INTO metric_rollups_hourly
		  (server_id, period_start, avg_cpu, avg_ram, avg_disk, avg_swap,
		   avg_load1, avg_load5, avg_load15, sample_count, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		serverID, rp.PeriodStart, rp.AvgCPU, rp.AvgRAM, rp.AvgDisk, rp.AvgSwap,
		rp.AvgLoad1, rp.AvgLoad5, rp.AvgLoad15, rp.SampleCount, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("analytics: insert hourly rollup: %w", err)
	}
	return nil
}

func (r SQLRepository) InsertDailyRollup(ctx context.Context, serverID int64, rp DailyRollup) error {
	_, err := r.conn.ExecContext(ctx,
		`INSERT INTO metric_rollups_daily
		  (server_id, period_start, avg_cpu, avg_ram, avg_disk, avg_swap,
		   avg_load1, avg_load5, avg_load15, sample_count, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		serverID, rp.PeriodStart, rp.AvgCPU, rp.AvgRAM, rp.AvgDisk, rp.AvgSwap,
		rp.AvgLoad1, rp.AvgLoad5, rp.AvgLoad15, rp.SampleCount, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("analytics: insert daily rollup: %w", err)
	}
	return nil
}

func (r SQLRepository) ListServerIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.conn.QueryContext(ctx, `SELECT DISTINCT server_id FROM system_snapshots`)
	if err != nil {
		return nil, fmt.Errorf("analytics: list server ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("analytics: scan server id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r SQLRepository) GetLatestTrafficForServer(ctx context.Context, serverID int64) ([]TrafficDay, []TrafficMonth, error) {
	row := r.conn.QueryRowContext(ctx,
		`SELECT daily_rows_json, monthly_rows_json
		 FROM vnstat_snapshots
		 WHERE server_id = ? AND available = 1
		 ORDER BY id DESC LIMIT 1`,
		serverID,
	)

	var dailyJSON, monthlyJSON string
	if err := row.Scan(&dailyJSON, &monthlyJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("analytics: get latest traffic: %w", err)
	}

	type rawRow struct {
		Label string `json:"label"`
		RX    int64  `json:"rx_bytes"`
		TX    int64  `json:"tx_bytes"`
		Total int64  `json:"total_bytes"`
	}

	var rawDaily []rawRow
	if err := json.Unmarshal([]byte(dailyJSON), &rawDaily); err != nil {
		return nil, nil, fmt.Errorf("analytics: unmarshal daily rows: %w", err)
	}
	var rawMonthly []rawRow
	if err := json.Unmarshal([]byte(monthlyJSON), &rawMonthly); err != nil {
		return nil, nil, fmt.Errorf("analytics: unmarshal monthly rows: %w", err)
	}

	days := make([]TrafficDay, 0, len(rawDaily))
	for _, d := range rawDaily {
		total := d.Total
		if total == 0 {
			total = d.RX + d.TX
		}
		days = append(days, TrafficDay{Label: d.Label, RX: d.RX, TX: d.TX, Total: total})
	}
	months := make([]TrafficMonth, 0, len(rawMonthly))
	for _, m := range rawMonthly {
		total := m.Total
		if total == 0 {
			total = m.RX + m.TX
		}
		months = append(months, TrafficMonth{Label: m.Label, RX: m.RX, TX: m.TX, Total: total})
	}
	return days, months, nil
}

// GetTrafficLimit reads the optional monthly download (RX) cap for a server.
// A missing row is reported as ok=false (unlimited), not an error.
func (r SQLRepository) GetTrafficLimit(ctx context.Context, serverID int64) (int64, bool, error) {
	var limitBytes int64
	err := r.conn.QueryRowContext(ctx,
		`SELECT monthly_limit_bytes FROM server_traffic_limits WHERE server_id = ? LIMIT 1`,
		serverID,
	).Scan(&limitBytes)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("analytics: get traffic limit: %w", err)
	}
	return limitBytes, true, nil
}

// SetTrafficLimit upserts the limit. It is portable across SQLite/MySQL: it
// updates the existing row and, when nothing was updated, inserts a new one —
// avoiding dialect-specific UPSERT syntax. The two statements run in one
// transaction so a concurrent writer can't slip between them.
func (r SQLRepository) SetTrafficLimit(ctx context.Context, serverID, limitBytes int64) error {
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("analytics: begin set traffic limit: %w", err)
	}

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx,
		`UPDATE server_traffic_limits SET monthly_limit_bytes = ?, updated_at = ? WHERE server_id = ?`,
		limitBytes, now, serverID,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("analytics: update traffic limit: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("analytics: traffic limit rows affected: %w", err)
	}
	if affected == 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO server_traffic_limits (server_id, monthly_limit_bytes, updated_at) VALUES (?, ?, ?)`,
			serverID, limitBytes, now,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("analytics: insert traffic limit: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("analytics: commit set traffic limit: %w", err)
	}
	return nil
}

// DeleteTrafficLimit clears a server's limit. A missing row is not an error.
func (r SQLRepository) DeleteTrafficLimit(ctx context.Context, serverID int64) error {
	if _, err := r.conn.ExecContext(ctx,
		`DELETE FROM server_traffic_limits WHERE server_id = ?`, serverID,
	); err != nil {
		return fmt.Errorf("analytics: delete traffic limit: %w", err)
	}
	return nil
}

func (r SQLRepository) DeleteRawBefore(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.conn.ExecContext(ctx,
		`DELETE FROM system_snapshots WHERE created_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("analytics: delete raw before %v: %w", before, err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func (r SQLRepository) DeleteHourlyBefore(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.conn.ExecContext(ctx,
		`DELETE FROM metric_rollups_hourly WHERE period_start < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("analytics: delete hourly before %v: %w", before, err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func (r SQLRepository) DeleteDailyBefore(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.conn.ExecContext(ctx,
		`DELETE FROM metric_rollups_daily WHERE period_start < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("analytics: delete daily before %v: %w", before, err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func (r SQLRepository) ListServerMetricSummaries(ctx context.Context, limit int) ([]ServerMetricSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.conn.QueryContext(ctx,
		`SELECT ss.server_id, s.name, s.country_code,
		        ss.cpu_usage, ss.ram_usage, ss.disk_usage, ss.swap_usage
		 FROM system_snapshots ss
		 JOIN servers s ON s.id = ss.server_id
		 JOIN (
		   SELECT server_id, MAX(id) AS latest_id
		   FROM system_snapshots
		   GROUP BY server_id
		 ) latest ON latest.latest_id = ss.id
		 ORDER BY ss.cpu_usage DESC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: list server metric summaries: %w", err)
	}
	defer rows.Close()

	var summaries []ServerMetricSummary
	for rows.Next() {
		var s ServerMetricSummary
		if err := rows.Scan(&s.ServerID, &s.ServerName, &s.CountryCode, &s.AvgCPU, &s.AvgRAM, &s.AvgDisk, &s.AvgSwap); err != nil {
			return nil, fmt.Errorf("analytics: scan server metric summary: %w", err)
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// ListServerTrafficSummaries returns the current-month RX/TX/total for every
// server with vnstat data. It does NOT apply a SQL LIMIT: the "top N" ordering
// is by monthly total, which lives inside the JSON blob and can't be sorted in
// SQL, so the caller sorts and truncates. (The previous LIMIT picked an
// arbitrary N rows before sorting, so the real top consumers could be dropped.)
func (r SQLRepository) ListServerTrafficSummaries(ctx context.Context, limit int) ([]ServerTrafficSummary, error) {
	rows, err := r.conn.QueryContext(ctx,
		`SELECT vs.server_id, s.name, s.country_code, vs.monthly_rows_json
		 FROM vnstat_snapshots vs
		 JOIN servers s ON s.id = vs.server_id
		 JOIN (
		   SELECT server_id, MAX(id) AS latest_id
		   FROM vnstat_snapshots
		   WHERE available = 1
		   GROUP BY server_id
		 ) latest ON latest.latest_id = vs.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: list server traffic summaries: %w", err)
	}
	defer rows.Close()

	currentMonth := time.Now().UTC().Format("2006-01")
	type rawRow struct {
		Label string `json:"label"`
		RX    int64  `json:"rx_bytes"`
		TX    int64  `json:"tx_bytes"`
		Total int64  `json:"total_bytes"`
	}

	var summaries []ServerTrafficSummary
	for rows.Next() {
		var serverID int64
		var serverName, countryCode, monthlyJSON string
		if err := rows.Scan(&serverID, &serverName, &countryCode, &monthlyJSON); err != nil {
			return nil, fmt.Errorf("analytics: scan server traffic summary: %w", err)
		}
		var monthlyRows []rawRow
		_ = json.Unmarshal([]byte(monthlyJSON), &monthlyRows)

		var rx, tx, monthBytes int64
		for _, m := range monthlyRows {
			if m.Label == currentMonth {
				rx, tx = m.RX, m.TX
				monthBytes = m.Total
				if monthBytes == 0 {
					monthBytes = m.RX + m.TX
				}
				break
			}
		}
		summaries = append(summaries, ServerTrafficSummary{
			ServerID:    serverID,
			ServerName:  serverName,
			CountryCode: countryCode,
			MonthRX:     rx,
			MonthTX:     tx,
			MonthBytes:  monthBytes,
			MonthLabel:  currentMonth,
		})
	}
	return summaries, rows.Err()
}

// parseDatabaseTime handles SQLite's flexible datetime storage (string or time.Time).
func parseDatabaseTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v.UTC(), nil
	case string:
		return parseTimeString(v)
	case []byte:
		return parseTimeString(string(v))
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
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time value %q", value)
}
