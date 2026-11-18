package terminal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

const (
	maxHistoryCommandBytes      = 512
	maxHistoryRequestBodyBytes  = 4 * 1024
	maxServerHistoryReturnLimit = 250
	maxGlobalHistoryReturnLimit = 250
	maxServerHistoryRetention   = 400
)

// HistoryItem represents one learned command entry returned to the terminal autocomplete UI.
type HistoryItem struct {
	Command string `json:"cmd"`
	Count   int    `json:"count"`
	TS      int64  `json:"ts"`
}

type historyRepository struct {
	conn *sql.DB
}

func newHistoryRepository(conn *sql.DB) historyRepository {
	return historyRepository{conn: conn}
}

func hashCommand(cmd string) string {
	sum := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(sum[:])
}

// sanitizeHistoryCommand normalises a command line submitted from the terminal
// prompt and reports whether it should be stored for future autocomplete.
func sanitizeHistoryCommand(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	// Honor shell HISTCONTROL=ignorespace convention.
	if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
		return "", false
	}
	if strings.ContainsAny(raw, "\r\n\x00\x1b") {
		return "", false
	}
	cmd := strings.TrimSpace(raw)
	if cmd == "" || len(cmd) > maxHistoryCommandBytes {
		return "", false
	}
	if len(cmd) < 2 && cmd != "w" {
		return "", false
	}
	lower := strings.ToLower(cmd)
	for _, secretHint := range []string{
		"password=",
		"passwd=",
		"passphrase=",
		"token=",
		"secret=",
		"api_key=",
		"apikey=",
		"--password",
		"--token",
		"--secret",
		"authorization:",
		"bearer ",
	} {
		if strings.Contains(lower, secretHint) {
			return "", false
		}
	}
	return cmd, true
}

func (r historyRepository) RecordCommand(ctx context.Context, serverID int64, raw string) (bool, error) {
	cmd, ok := sanitizeHistoryCommand(raw)
	if !ok {
		return false, nil
	}

	hash := hashCommand(cmd)
	now := time.Now().UTC()

	var upsertErr error
	for attempt := 0; attempt < 2; attempt++ {
		upsertErr = r.upsertCommand(ctx, serverID, hash, cmd, now)
		if upsertErr == nil {
			break
		}
	}
	if upsertErr != nil {
		return false, upsertErr
	}

	// Best-effort retention cap per server (portable across SQLite & MySQL).
	r.trimServerHistory(ctx, serverID)
	return true, nil
}

func (r historyRepository) upsertCommand(ctx context.Context, serverID int64, hash string, cmd string, now time.Time) error {
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("terminal history: begin tx: %w", err)
	}

	var existingID int64
	scanErr := tx.QueryRowContext(
		ctx,
		`SELECT id FROM terminal_command_history WHERE server_id = ? AND command_hash = ? LIMIT 1`,
		serverID, hash,
	).Scan(&existingID)

	switch {
	case scanErr == nil:
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE terminal_command_history
			 SET command_text = ?, use_count = use_count + 1, last_used_at = ?
			 WHERE id = ?`,
			cmd, now, existingID,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("terminal history: update: %w", err)
		}
	case errors.Is(scanErr, sql.ErrNoRows):
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO terminal_command_history (server_id, command_hash, command_text, use_count, last_used_at)
			 VALUES (?, ?, ?, 1, ?)`,
			serverID, hash, cmd, now,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("terminal history: insert: %w", err)
		}
	default:
		_ = tx.Rollback()
		return fmt.Errorf("terminal history: lookup: %w", scanErr)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("terminal history: commit: %w", err)
	}
	return nil
}

func (r historyRepository) trimServerHistory(ctx context.Context, serverID int64) {
	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT id FROM terminal_command_history
		 WHERE server_id = ?
		 ORDER BY last_used_at DESC, id DESC
		 LIMIT 100 OFFSET ?`,
		serverID, maxServerHistoryRetention,
	)
	if err != nil {
		return
	}
	var staleIDs []int64
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr == nil {
			staleIDs = append(staleIDs, id)
		}
	}
	_ = rows.Close()

	for _, id := range staleIDs {
		_, _ = r.conn.ExecContext(ctx, `DELETE FROM terminal_command_history WHERE id = ?`, id)
	}
}

func (r historyRepository) ListForServer(ctx context.Context, serverID int64) (serverItems []HistoryItem, globalItems []HistoryItem, err error) {
	serverItems = make([]HistoryItem, 0, 32)
	seenServer := make(map[string]struct{}, 64)

	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT command_text, use_count, last_used_at
		 FROM terminal_command_history
		 WHERE server_id = ?
		 ORDER BY last_used_at DESC, use_count DESC, id DESC
		 LIMIT ?`,
		serverID, maxServerHistoryReturnLimit,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("terminal history: query server: %w", err)
	}
	for rows.Next() {
		var cmd string
		var count int
		var tsRaw any
		if scanErr := rows.Scan(&cmd, &count, &tsRaw); scanErr != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("terminal history: scan server: %w", scanErr)
		}
		clean, ok := sanitizeHistoryCommand(cmd)
		if !ok {
			continue
		}
		if _, dup := seenServer[clean]; dup {
			continue
		}
		seenServer[clean] = struct{}{}
		serverItems = append(serverItems, HistoryItem{
			Command: clean,
			Count:   count,
			TS:      parseHistoryTimestampMs(tsRaw),
		})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return nil, nil, fmt.Errorf("terminal history: iterate server: %w", rowsErr)
	}
	_ = rows.Close()

	// Also seed from recent non-interactive commands executed on this server in
	// the Commands module (best-effort — never fail if command_history is empty).
	cmdRows, cmdErr := r.conn.QueryContext(
		ctx,
		`SELECT command_text, executed_at
		 FROM command_history
		 WHERE server_id = ?
		 ORDER BY id DESC
		 LIMIT 80`,
		serverID,
	)
	if cmdErr == nil {
		for cmdRows.Next() {
			var cmd string
			var tsRaw any
			if scanErr := cmdRows.Scan(&cmd, &tsRaw); scanErr != nil {
				continue
			}
			clean, ok := sanitizeHistoryCommand(cmd)
			if !ok {
				continue
			}
			if _, dup := seenServer[clean]; dup {
				continue
			}
			seenServer[clean] = struct{}{}
			serverItems = append(serverItems, HistoryItem{
				Command: clean,
				Count:   1,
				TS:      parseHistoryTimestampMs(tsRaw),
			})
			if len(serverItems) >= maxServerHistoryReturnLimit {
				break
			}
		}
		_ = cmdRows.Close()
	}

	globalItems = make([]HistoryItem, 0, 32)
	seenGlobal := make(map[string]int, 64)
	gRows, gErr := r.conn.QueryContext(
		ctx,
		`SELECT command_text, use_count, last_used_at
		 FROM terminal_command_history
		 WHERE server_id <> ?
		 ORDER BY last_used_at DESC, use_count DESC, id DESC
		 LIMIT ?`,
		serverID, maxGlobalHistoryReturnLimit,
	)
	if gErr == nil {
		for gRows.Next() {
			var cmd string
			var count int
			var tsRaw any
			if scanErr := gRows.Scan(&cmd, &count, &tsRaw); scanErr != nil {
				continue
			}
			clean, ok := sanitizeHistoryCommand(cmd)
			if !ok {
				continue
			}
			ts := parseHistoryTimestampMs(tsRaw)
			if idx, exists := seenGlobal[clean]; exists {
				globalItems[idx].Count += count
				if ts > globalItems[idx].TS {
					globalItems[idx].TS = ts
				}
				continue
			}
			seenGlobal[clean] = len(globalItems)
			globalItems = append(globalItems, HistoryItem{
				Command: clean,
				Count:   count,
				TS:      ts,
			})
		}
		_ = gRows.Close()
	}

	return serverItems, globalItems, nil
}

func (r historyRepository) ClearServerHistory(ctx context.Context, serverID int64) error {
	if _, err := r.conn.ExecContext(
		ctx,
		`DELETE FROM terminal_command_history WHERE server_id = ?`,
		serverID,
	); err != nil {
		return fmt.Errorf("terminal history: clear server %d: %w", serverID, err)
	}
	return nil
}

func parseHistoryTimestampMs(value any) int64 {
	var parsed time.Time
	switch typed := value.(type) {
	case time.Time:
		parsed = typed.UTC()
	case string:
		parsed = parseHistoryTimeString(typed)
	case []byte:
		parsed = parseHistoryTimeString(string(typed))
	default:
		return 0
	}
	if parsed.IsZero() {
		return 0
	}
	return parsed.UnixMilli()
}

func parseHistoryTimeString(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ── HTTP Handler ──────────────────────────────────────────────────────────────

type historyHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	repo       historyRepository
}

func newHistoryHandler(deps module.Dependencies, serverRepo servers.Repository) historyHandler {
	return historyHandler{
		deps:       deps,
		serverRepo: serverRepo,
		repo:       newHistoryRepository(deps.Database.SQL),
	}
}

func (h historyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathServerID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Invalid server ID"})
		return
	}

	if _, err := h.serverRepo.GetByID(r.Context(), serverID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, servers.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"ok": false, "error": "Server not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		serverItems, globalItems, err := h.repo.ListForServer(r.Context(), serverID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to load history"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"server_history": serverItems,
			"global_history": globalItems,
		})

	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxHistoryRequestBodyBytes)
		var payload struct {
			Command string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Invalid JSON payload"})
			return
		}
		recorded, err := h.repo.RecordCommand(r.Context(), serverID, payload.Command)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to record command"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"recorded": recorded,
		})

	case http.MethodDelete:
		if err := h.repo.ClearServerHistory(r.Context(), serverID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Failed to clear history"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
