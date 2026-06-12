package alerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SQLRepository is a portable SQLite/MySQL implementation of Repository.
type SQLRepository struct {
	conn *sql.DB
}

func NewSQLRepository(conn *sql.DB) SQLRepository {
	return SQLRepository{conn: conn}
}

const ruleColumns = `id, server_id, metric, comparator, threshold, consecutive_hits,
	cooldown_seconds, severity, channel_id, enabled, note, created_at, updated_at`

// ── Rules ────────────────────────────────────────────────────────────────────

func (r SQLRepository) CreateRule(ctx context.Context, rule Rule) (Rule, error) {
	rule = normalizeRule(rule)
	now := time.Now().UTC()

	result, err := r.conn.ExecContext(
		ctx,
		`INSERT INTO alert_rules
			(server_id, metric, comparator, threshold, consecutive_hits, cooldown_seconds,
			 severity, channel_id, enabled, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableInt64(rule.ServerID),
		rule.Metric,
		rule.Comparator,
		rule.Threshold,
		rule.ConsecutiveHits,
		rule.CooldownSeconds,
		rule.Severity,
		nullableInt64(rule.ChannelID),
		boolToInt(rule.Enabled),
		rule.Note,
		now,
		now,
	)
	if err != nil {
		return Rule{}, fmt.Errorf("alerts: create rule: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Rule{}, fmt.Errorf("alerts: create rule last insert id: %w", err)
	}

	return r.GetRule(ctx, id)
}

func (r SQLRepository) GetRule(ctx context.Context, id int64) (Rule, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT `+ruleColumns+` FROM alert_rules WHERE id = ? LIMIT 1`,
		id,
	)

	rule, err := scanRule(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Rule{}, ErrNotFound
		}
		return Rule{}, fmt.Errorf("alerts: get rule %d: %w", id, err)
	}
	return rule, nil
}

func (r SQLRepository) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT `+ruleColumns+` FROM alert_rules ORDER BY id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("alerts: list rules: %w", err)
	}
	defer rows.Close()

	return scanRules(rows)
}

func (r SQLRepository) ListEnabledRulesForServer(ctx context.Context, serverID int64) ([]Rule, error) {
	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT `+ruleColumns+`
		 FROM alert_rules
		 WHERE enabled = 1 AND (server_id IS NULL OR server_id = ?)
		 ORDER BY id ASC`,
		serverID,
	)
	if err != nil {
		return nil, fmt.Errorf("alerts: list enabled rules for server %d: %w", serverID, err)
	}
	defer rows.Close()

	return scanRules(rows)
}

func (r SQLRepository) UpdateRule(ctx context.Context, rule Rule) (Rule, error) {
	rule = normalizeRule(rule)
	rule.UpdatedAt = time.Now().UTC()

	result, err := r.conn.ExecContext(
		ctx,
		`UPDATE alert_rules
		 SET server_id = ?, metric = ?, comparator = ?, threshold = ?, consecutive_hits = ?,
		     cooldown_seconds = ?, severity = ?, channel_id = ?, enabled = ?, note = ?, updated_at = ?
		 WHERE id = ?`,
		nullableInt64(rule.ServerID),
		rule.Metric,
		rule.Comparator,
		rule.Threshold,
		rule.ConsecutiveHits,
		rule.CooldownSeconds,
		rule.Severity,
		nullableInt64(rule.ChannelID),
		boolToInt(rule.Enabled),
		rule.Note,
		rule.UpdatedAt,
		rule.ID,
	)
	if err != nil {
		return Rule{}, fmt.Errorf("alerts: update rule %d: %w", rule.ID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Rule{}, fmt.Errorf("alerts: update rule rows affected %d: %w", rule.ID, err)
	}
	if affected == 0 {
		return Rule{}, ErrNotFound
	}

	return r.GetRule(ctx, rule.ID)
}

func (r SQLRepository) DeleteRule(ctx context.Context, id int64) error {
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("alerts: begin delete rule transaction: %w", err)
	}

	// alert_events.rule_id is ON DELETE SET NULL (foreign keys are enforced);
	// detach explicitly so the intent is clear and stays correct if the FK action
	// ever changes, matching servers.Delete's explicit-cascade style.
	if _, err := tx.ExecContext(ctx, `UPDATE alert_events SET rule_id = NULL WHERE rule_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("alerts: detach events from rule %d: %w", id, err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("alerts: delete rule %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("alerts: delete rule rows affected %d: %w", id, err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("alerts: commit delete rule transaction: %w", err)
	}
	return nil
}

// ── Channels ─────────────────────────────────────────────────────────────────

const channelColumns = `id, kind, name, chat_id, message_template, enabled, created_at, updated_at`

func (r SQLRepository) CreateChannel(ctx context.Context, channel Channel) (Channel, error) {
	channel = normalizeChannel(channel)
	now := time.Now().UTC()

	result, err := r.conn.ExecContext(
		ctx,
		`INSERT INTO alert_channels (kind, name, chat_id, message_template, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		channel.Kind,
		channel.Name,
		channel.ChatID,
		channel.MessageTemplate,
		boolToInt(channel.Enabled),
		now,
		now,
	)
	if err != nil {
		return Channel{}, fmt.Errorf("alerts: create channel: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Channel{}, fmt.Errorf("alerts: create channel last insert id: %w", err)
	}

	return r.GetChannel(ctx, id)
}

func (r SQLRepository) GetChannel(ctx context.Context, id int64) (Channel, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT `+channelColumns+` FROM alert_channels WHERE id = ? LIMIT 1`,
		id,
	)

	channel, err := scanChannel(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Channel{}, ErrNotFound
		}
		return Channel{}, fmt.Errorf("alerts: get channel %d: %w", id, err)
	}
	return channel, nil
}

func (r SQLRepository) ListChannels(ctx context.Context) ([]Channel, error) {
	return r.queryChannels(ctx, `SELECT `+channelColumns+` FROM alert_channels ORDER BY id DESC`)
}

func (r SQLRepository) ListEnabledChannels(ctx context.Context) ([]Channel, error) {
	return r.queryChannels(ctx, `SELECT `+channelColumns+` FROM alert_channels WHERE enabled = 1 ORDER BY id ASC`)
}

func (r SQLRepository) queryChannels(ctx context.Context, query string, args ...any) ([]Channel, error) {
	rows, err := r.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("alerts: list channels: %w", err)
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("alerts: scan channel: %w", err)
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func (r SQLRepository) UpdateChannel(ctx context.Context, channel Channel) (Channel, error) {
	channel = normalizeChannel(channel)
	channel.UpdatedAt = time.Now().UTC()

	result, err := r.conn.ExecContext(
		ctx,
		`UPDATE alert_channels
		 SET kind = ?, name = ?, chat_id = ?, message_template = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		channel.Kind,
		channel.Name,
		channel.ChatID,
		channel.MessageTemplate,
		boolToInt(channel.Enabled),
		channel.UpdatedAt,
		channel.ID,
	)
	if err != nil {
		return Channel{}, fmt.Errorf("alerts: update channel %d: %w", channel.ID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Channel{}, fmt.Errorf("alerts: update channel rows affected %d: %w", channel.ID, err)
	}
	if affected == 0 {
		return Channel{}, ErrNotFound
	}

	return r.GetChannel(ctx, channel.ID)
}

func (r SQLRepository) DeleteChannel(ctx context.Context, id int64) error {
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("alerts: begin delete channel transaction: %w", err)
	}

	// alert_rules.channel_id is ON DELETE SET NULL (foreign keys are enforced);
	// detach explicitly so the intent is clear and stays correct if the FK action
	// ever changes, matching servers.Delete's explicit-cascade style.
	if _, err := tx.ExecContext(ctx, `UPDATE alert_rules SET channel_id = NULL WHERE channel_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("alerts: detach rules from channel %d: %w", id, err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = ?`, id)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("alerts: delete channel %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("alerts: delete channel rows affected %d: %w", id, err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("alerts: commit delete channel transaction: %w", err)
	}
	return nil
}

// ── Silences ─────────────────────────────────────────────────────────────────

const silenceColumns = `id, server_id, metric, reason, expires_at, created_at`

// CreateSilence inserts a silence, or refreshes the existing one for the same
// (server_id, metric) pair. The UNIQUE constraint makes a plain insert fail on a
// repeat mute, so this upserts in a single transaction without relying on
// engine-specific ON CONFLICT syntax.
func (r SQLRepository) CreateSilence(ctx context.Context, silence Silence) (Silence, error) {
	silence = normalizeSilence(silence)
	now := time.Now().UTC()

	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return Silence{}, fmt.Errorf("alerts: begin create silence transaction: %w", err)
	}

	var existingID int64
	err = tx.QueryRowContext(
		ctx,
		`SELECT id FROM alert_silences WHERE server_id = ? AND metric = ? LIMIT 1`,
		silence.ServerID,
		silence.Metric,
	).Scan(&existingID)

	switch {
	case err == nil:
		if _, updateErr := tx.ExecContext(
			ctx,
			`UPDATE alert_silences SET reason = ?, expires_at = ?, created_at = ? WHERE id = ?`,
			silence.Reason,
			nullableTime(silence.ExpiresAt),
			now,
			existingID,
		); updateErr != nil {
			_ = tx.Rollback()
			return Silence{}, fmt.Errorf("alerts: refresh silence %d: %w", existingID, updateErr)
		}
	case errors.Is(err, sql.ErrNoRows):
		result, insertErr := tx.ExecContext(
			ctx,
			`INSERT INTO alert_silences (server_id, metric, reason, expires_at, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			silence.ServerID,
			silence.Metric,
			silence.Reason,
			nullableTime(silence.ExpiresAt),
			now,
		)
		if insertErr != nil {
			_ = tx.Rollback()
			return Silence{}, fmt.Errorf("alerts: create silence: %w", insertErr)
		}
		existingID, insertErr = result.LastInsertId()
		if insertErr != nil {
			_ = tx.Rollback()
			return Silence{}, fmt.Errorf("alerts: create silence last insert id: %w", insertErr)
		}
	default:
		_ = tx.Rollback()
		return Silence{}, fmt.Errorf("alerts: look up existing silence: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Silence{}, fmt.Errorf("alerts: commit create silence transaction: %w", err)
	}

	return r.GetSilence(ctx, existingID)
}

func (r SQLRepository) GetSilence(ctx context.Context, id int64) (Silence, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT `+silenceColumns+` FROM alert_silences WHERE id = ? LIMIT 1`,
		id,
	)

	silence, err := scanSilence(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Silence{}, ErrNotFound
		}
		return Silence{}, fmt.Errorf("alerts: get silence %d: %w", id, err)
	}
	return silence, nil
}

func (r SQLRepository) ListSilences(ctx context.Context) ([]Silence, error) {
	return r.querySilences(ctx, `SELECT `+silenceColumns+` FROM alert_silences ORDER BY id DESC`)
}

func (r SQLRepository) ListSilencesForServer(ctx context.Context, serverID int64) ([]Silence, error) {
	return r.querySilences(
		ctx,
		`SELECT `+silenceColumns+` FROM alert_silences WHERE server_id = ? ORDER BY id DESC`,
		serverID,
	)
}

func (r SQLRepository) querySilences(ctx context.Context, query string, args ...any) ([]Silence, error) {
	rows, err := r.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("alerts: list silences: %w", err)
	}
	defer rows.Close()

	var silences []Silence
	for rows.Next() {
		silence, err := scanSilence(rows)
		if err != nil {
			return nil, fmt.Errorf("alerts: scan silence: %w", err)
		}
		silences = append(silences, silence)
	}
	return silences, rows.Err()
}

func (r SQLRepository) DeleteSilence(ctx context.Context, id int64) error {
	result, err := r.conn.ExecContext(ctx, `DELETE FROM alert_silences WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("alerts: delete silence %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("alerts: delete silence rows affected %d: %w", id, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r SQLRepository) IsSilenced(ctx context.Context, serverID int64, metric string) (bool, error) {
	var exists int
	err := r.conn.QueryRowContext(
		ctx,
		`SELECT 1 FROM alert_silences
		 WHERE server_id = ?
		   AND (metric = ? OR metric = ?)
		   AND (expires_at IS NULL OR expires_at > ?)
		 LIMIT 1`,
		serverID,
		normalizeMetric(metric),
		MetricAll,
		time.Now().UTC(),
	).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("alerts: check silence for server %d metric %q: %w", serverID, metric, err)
	}
	return true, nil
}

// ── Events ───────────────────────────────────────────────────────────────────

const eventColumns = `id, rule_id, server_id, metric, observed_value, threshold,
	severity, state, fired_at, resolved_at, notified_at`

func (r SQLRepository) CreateEvent(ctx context.Context, event Event) (Event, error) {
	if event.FiredAt.IsZero() {
		event.FiredAt = time.Now().UTC()
	}
	if event.State == "" {
		event.State = EventStateFiring
	}

	result, err := r.conn.ExecContext(
		ctx,
		`INSERT INTO alert_events
			(rule_id, server_id, metric, observed_value, threshold, severity, state, fired_at, resolved_at, notified_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableInt64(event.RuleID),
		event.ServerID,
		event.Metric,
		event.ObservedValue,
		event.Threshold,
		event.Severity,
		event.State,
		event.FiredAt.UTC(),
		nullableTime(event.ResolvedAt),
		nullableTime(event.NotifiedAt),
	)
	if err != nil {
		return Event{}, fmt.Errorf("alerts: create event: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Event{}, fmt.Errorf("alerts: create event last insert id: %w", err)
	}

	return r.getEvent(ctx, id)
}

func (r SQLRepository) getEvent(ctx context.Context, id int64) (Event, error) {
	row := r.conn.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM alert_events WHERE id = ? LIMIT 1`, id)
	event, err := scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, ErrNotFound
		}
		return Event{}, fmt.Errorf("alerts: get event %d: %w", id, err)
	}
	return event, nil
}

func (r SQLRepository) GetOpenEvent(ctx context.Context, ruleID, serverID int64) (Event, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT `+eventColumns+`
		 FROM alert_events
		 WHERE rule_id = ? AND server_id = ? AND state = ? AND resolved_at IS NULL
		 ORDER BY id DESC
		 LIMIT 1`,
		ruleID,
		serverID,
		EventStateFiring,
	)
	event, err := scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, ErrNotFound
		}
		return Event{}, fmt.Errorf("alerts: get open event for rule %d server %d: %w", ruleID, serverID, err)
	}
	return event, nil
}

func (r SQLRepository) MarkEventNotified(ctx context.Context, eventID int64, at time.Time) error {
	result, err := r.conn.ExecContext(
		ctx,
		`UPDATE alert_events SET notified_at = ? WHERE id = ?`,
		at.UTC(),
		eventID,
	)
	if err != nil {
		return fmt.Errorf("alerts: mark event %d notified: %w", eventID, err)
	}
	return rowsAffectedOrNotFound(result)
}

func (r SQLRepository) ResolveEvent(ctx context.Context, eventID int64, at time.Time) error {
	result, err := r.conn.ExecContext(
		ctx,
		`UPDATE alert_events SET state = ?, resolved_at = ? WHERE id = ?`,
		EventStateResolved,
		at.UTC(),
		eventID,
	)
	if err != nil {
		return fmt.Errorf("alerts: resolve event %d: %w", eventID, err)
	}
	return rowsAffectedOrNotFound(result)
}

func (r SQLRepository) ListRecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT `+eventColumns+` FROM alert_events ORDER BY id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("alerts: list recent events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("alerts: scan event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r SQLRepository) CountEvents(ctx context.Context) (int, error) {
	var total int
	err := r.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_events`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("alerts: count events: %w", err)
	}
	return total, nil
}

func (r SQLRepository) ListEventsPage(ctx context.Context, limit, offset int) ([]Event, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT `+eventColumns+` FROM alert_events ORDER BY id DESC LIMIT ? OFFSET ?`,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("alerts: list events page: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("alerts: scan event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// ── Streaks ──────────────────────────────────────────────────────────────────

func (r SQLRepository) GetStreak(ctx context.Context, ruleID, serverID int64) (int, error) {
	var streak int
	err := r.conn.QueryRowContext(
		ctx,
		`SELECT streak FROM alert_rule_streaks WHERE rule_id = ? AND server_id = ? LIMIT 1`,
		ruleID, serverID,
	).Scan(&streak)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("alerts: get streak rule %d server %d: %w", ruleID, serverID, err)
	}
	return streak, nil
}

func (r SQLRepository) SetStreak(ctx context.Context, ruleID, serverID int64, streak int) error {
	now := time.Now().UTC()

	if streak <= 0 {
		_, err := r.conn.ExecContext(
			ctx,
			`DELETE FROM alert_rule_streaks WHERE rule_id = ? AND server_id = ?`,
			ruleID, serverID,
		)
		if err != nil {
			return fmt.Errorf("alerts: delete streak rule %d server %d: %w", ruleID, serverID, err)
		}
		return nil
	}

	// Portable upsert: update if the row exists, insert otherwise. This works on
	// both SQLite and MySQL without engine-specific ON CONFLICT / ON DUPLICATE KEY
	// syntax, matching the pattern used in CreateSilence.
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("alerts: begin set streak transaction: %w", err)
	}

	var existing int
	scanErr := tx.QueryRowContext(
		ctx,
		`SELECT 1 FROM alert_rule_streaks WHERE rule_id = ? AND server_id = ? LIMIT 1`,
		ruleID, serverID,
	).Scan(&existing)

	switch {
	case scanErr == nil:
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE alert_rule_streaks SET streak = ?, updated_at = ? WHERE rule_id = ? AND server_id = ?`,
			streak, now, ruleID, serverID,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("alerts: update streak rule %d server %d: %w", ruleID, serverID, err)
		}
	case errors.Is(scanErr, sql.ErrNoRows):
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO alert_rule_streaks (rule_id, server_id, streak, updated_at) VALUES (?, ?, ?, ?)`,
			ruleID, serverID, streak, now,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("alerts: insert streak rule %d server %d: %w", ruleID, serverID, err)
		}
	default:
		_ = tx.Rollback()
		return fmt.Errorf("alerts: check streak rule %d server %d: %w", ruleID, serverID, scanErr)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("alerts: commit set streak transaction: %w", err)
	}
	return nil
}

func (r SQLRepository) ListStreaksForRules(ctx context.Context, ruleIDs []int64) (map[streakKey]int, error) {
	if len(ruleIDs) == 0 {
		return map[streakKey]int{}, nil
	}

	placeholders := strings.Repeat("?,", len(ruleIDs))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(ruleIDs))
	for i, id := range ruleIDs {
		args[i] = id
	}

	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT rule_id, server_id, streak FROM alert_rule_streaks WHERE rule_id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("alerts: list streaks: %w", err)
	}
	defer rows.Close()

	out := map[streakKey]int{}
	for rows.Next() {
		var ruleID, serverID int64
		var streak int
		if err := rows.Scan(&ruleID, &serverID, &streak); err != nil {
			return nil, fmt.Errorf("alerts: scan streak: %w", err)
		}
		out[streakKey{ruleID: ruleID, serverID: serverID}] = streak
	}
	return out, rows.Err()
}

func rowsAffectedOrNotFound(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("alerts: rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanEvent(scanner rowScanner) (Event, error) {
	var (
		event       Event
		ruleID      sql.NullInt64
		resolvedRaw any
		notifiedRaw any
		firedRaw    any
	)

	if err := scanner.Scan(
		&event.ID,
		&ruleID,
		&event.ServerID,
		&event.Metric,
		&event.ObservedValue,
		&event.Threshold,
		&event.Severity,
		&event.State,
		&firedRaw,
		&resolvedRaw,
		&notifiedRaw,
	); err != nil {
		return Event{}, err
	}

	if ruleID.Valid {
		value := ruleID.Int64
		event.RuleID = &value
	}

	var err error
	if event.FiredAt, err = parseDatabaseTime(firedRaw); err != nil {
		return Event{}, fmt.Errorf("parse fired_at: %w", err)
	}
	if resolvedAt, err := parseDatabaseTime(resolvedRaw); err != nil {
		return Event{}, fmt.Errorf("parse resolved_at: %w", err)
	} else if !resolvedAt.IsZero() {
		event.ResolvedAt = &resolvedAt
	}
	if notifiedAt, err := parseDatabaseTime(notifiedRaw); err != nil {
		return Event{}, fmt.Errorf("parse notified_at: %w", err)
	} else if !notifiedAt.IsZero() {
		event.NotifiedAt = &notifiedAt
	}

	return event, nil
}

// ── Scanning & helpers ───────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRules(rows *sql.Rows) ([]Rule, error) {
	var rules []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("alerts: scan rule: %w", err)
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func scanRule(scanner rowScanner) (Rule, error) {
	var (
		rule         Rule
		serverID     sql.NullInt64
		channelID    sql.NullInt64
		enabled      int64
		createdAtRaw any
		updatedAtRaw any
	)

	if err := scanner.Scan(
		&rule.ID,
		&serverID,
		&rule.Metric,
		&rule.Comparator,
		&rule.Threshold,
		&rule.ConsecutiveHits,
		&rule.CooldownSeconds,
		&rule.Severity,
		&channelID,
		&enabled,
		&rule.Note,
		&createdAtRaw,
		&updatedAtRaw,
	); err != nil {
		return Rule{}, err
	}

	if serverID.Valid {
		value := serverID.Int64
		rule.ServerID = &value
	}
	if channelID.Valid {
		value := channelID.Int64
		rule.ChannelID = &value
	}
	rule.Enabled = enabled != 0

	var err error
	if rule.CreatedAt, err = parseDatabaseTime(createdAtRaw); err != nil {
		return Rule{}, fmt.Errorf("parse created_at: %w", err)
	}
	if rule.UpdatedAt, err = parseDatabaseTime(updatedAtRaw); err != nil {
		return Rule{}, fmt.Errorf("parse updated_at: %w", err)
	}

	return rule, nil
}

func scanChannel(scanner rowScanner) (Channel, error) {
	var (
		channel      Channel
		enabled      int64
		createdAtRaw any
		updatedAtRaw any
	)

	if err := scanner.Scan(
		&channel.ID,
		&channel.Kind,
		&channel.Name,
		&channel.ChatID,
		&channel.MessageTemplate,
		&enabled,
		&createdAtRaw,
		&updatedAtRaw,
	); err != nil {
		return Channel{}, err
	}

	channel.Enabled = enabled != 0

	var err error
	if channel.CreatedAt, err = parseDatabaseTime(createdAtRaw); err != nil {
		return Channel{}, fmt.Errorf("parse created_at: %w", err)
	}
	if channel.UpdatedAt, err = parseDatabaseTime(updatedAtRaw); err != nil {
		return Channel{}, fmt.Errorf("parse updated_at: %w", err)
	}

	return channel, nil
}

func scanSilence(scanner rowScanner) (Silence, error) {
	var (
		silence      Silence
		expiresRaw   any
		createdAtRaw any
	)

	if err := scanner.Scan(
		&silence.ID,
		&silence.ServerID,
		&silence.Metric,
		&silence.Reason,
		&expiresRaw,
		&createdAtRaw,
	); err != nil {
		return Silence{}, err
	}

	expiresAt, err := parseDatabaseTime(expiresRaw)
	if err != nil {
		return Silence{}, fmt.Errorf("parse expires_at: %w", err)
	}
	if !expiresAt.IsZero() {
		silence.ExpiresAt = &expiresAt
	}

	if silence.CreatedAt, err = parseDatabaseTime(createdAtRaw); err != nil {
		return Silence{}, fmt.Errorf("parse created_at: %w", err)
	}

	return silence, nil
}

func normalizeRule(rule Rule) Rule {
	rule.Metric = normalizeMetric(rule.Metric)
	rule.Comparator = strings.ToLower(strings.TrimSpace(rule.Comparator))
	rule.Severity = strings.ToLower(strings.TrimSpace(rule.Severity))
	rule.Note = strings.TrimSpace(rule.Note)

	if rule.Comparator == "" {
		rule.Comparator = ComparatorGTE
	}
	if rule.Severity == "" {
		rule.Severity = SeverityWarning
	}
	if rule.ConsecutiveHits < 1 {
		rule.ConsecutiveHits = 1
	}
	if rule.CooldownSeconds < 0 {
		rule.CooldownSeconds = 0
	}
	return rule
}

func normalizeChannel(channel Channel) Channel {
	channel.Kind = strings.ToLower(strings.TrimSpace(channel.Kind))
	channel.Name = strings.TrimSpace(channel.Name)
	channel.ChatID = strings.TrimSpace(channel.ChatID)
	channel.MessageTemplate = strings.TrimSpace(channel.MessageTemplate)
	if channel.Kind == "" {
		channel.Kind = ChannelKindTelegram
	}
	return channel
}

func normalizeSilence(silence Silence) Silence {
	silence.Metric = normalizeMetric(silence.Metric)
	silence.Reason = strings.TrimSpace(silence.Reason)
	if silence.ExpiresAt != nil {
		utc := silence.ExpiresAt.UTC()
		silence.ExpiresAt = &utc
	}
	return silence
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
