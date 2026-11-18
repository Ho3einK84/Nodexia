package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/backup"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

const multipartOverheadBytes = 256 << 10 // headroom for form fields alongside max archive

// BackupExport handles POST /ops/backup/export. It streams a freshly generated
// backup as a file download; nothing is written to disk server-side. Secrets
// and an optional encryption passphrase are opt-in form fields.
func (h DiagnosticsHandler) BackupExport(w http.ResponseWriter, r *http.Request) {
	if h.database == nil || h.database.SQL == nil {
		h.renderPage(w, r, http.StatusServiceUnavailable, "error", h.tr(r, "backup.flash.unavailable"))
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderPage(w, r, http.StatusBadRequest, "error", h.tr(r, "backup.flash.export_failed", "error", err.Error()))
		return
	}

	includeSecrets := r.PostFormValue("include_secrets") == "on"
	passphrase := r.PostFormValue("passphrase")

	opts := backup.ExportOptions{
		IncludeSecrets: includeSecrets,
		Passphrase:     passphrase,
		AppVersion:     h.config.Version,
		Driver:         h.config.Database.Driver,
	}
	if includeSecrets {
		opts.Env = h.secretEnv()
	}

	data, err := backup.Export(r.Context(), h.database.SQL, opts)
	if err != nil {
		h.renderPage(w, r, http.StatusInternalServerError, "error", h.tr(r, "backup.flash.export_failed", "error", err.Error()))
		return
	}

	filename := "nodexia-backup-" + time.Now().UTC().Format("20060102-150405") + ".json"
	contentType := "application/json"
	if passphrase != "" {
		filename += ".enc"
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// BackupInspect handles POST /ops/backup/inspect. It parses and validates an
// uploaded backup file without modifying the database and returns JSON metadata,
// secret-redaction status, and per-table row counts for pre-restore preview.
func (h DiagnosticsHandler) BackupInspect(w http.ResponseWriter, r *http.Request) {
	if h.database == nil || h.database.SQL == nil {
		writeBackupJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"code":  "unavailable",
			"error": h.tr(r, "backup.flash.unavailable"),
		})
		return
	}

	if status, code, msg := parseBackupMultipart(w, r); status != 0 {
		writeBackupJSON(w, status, map[string]any{"ok": false, "code": code, "error": msg})
		return
	}

	raw, status, code, msg := readUploadedBackupFile(r, h)
	if status != 0 {
		writeBackupJSON(w, status, map[string]any{"ok": false, "code": code, "error": msg})
		return
	}

	outerFormat, _ := backup.PeekFormat(raw)
	isEncrypted := outerFormat == backup.FormatEncrypted

	archive, err := backup.Inspect(raw, r.PostFormValue("passphrase"))
	if err != nil {
		errStatus := http.StatusBadRequest
		if errors.Is(err, backup.ErrTooLarge) {
			errStatus = http.StatusRequestEntityTooLarge
		}
		if errors.Is(err, backup.ErrPassphraseRequired) || errors.Is(err, backup.ErrWrongPassphrase) {
			isEncrypted = true
		}
		writeBackupJSON(w, errStatus, map[string]any{
			"ok":        false,
			"code":      inspectErrorCode(err),
			"encrypted": isEncrypted,
			"error":     h.inspectErrorMessage(r, err),
		})
		return
	}

	summary := archive.Summary()
	writeBackupJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"format":           outerFormat,
		"encrypted":        isEncrypted,
		"version":          archive.Version,
		"created_at":       archive.CreatedAt,
		"app_version":      archive.AppVersion,
		"source_driver":    archive.SourceDriver,
		"includes_secrets": archive.IncludesSecrets,
		"counts": map[string]int{
			"servers":               summary.Servers,
			"server_tags":           summary.ServerTags,
			"server_traffic_limits": summary.ServerTrafficLimits,
			"traffic_limit_rules":   summary.TrafficLimitRules,
			"alert_channels":        summary.AlertChannels,
			"alert_rules":           summary.AlertRules,
			"alert_silences":        summary.AlertSilences,
			"install_metadata":      summary.InstallMetadata,
			"env_keys":              summary.EnvKeys,
		},
	})
}

// BackupImport handles POST /ops/backup/import. It validates the uploaded file,
// requires an explicit overwrite confirmation, and applies the restore inside a
// single transaction. The result (or a precise error) is shown as a flash on
// the diagnostics page.
func (h DiagnosticsHandler) BackupImport(w http.ResponseWriter, r *http.Request) {
	if h.database == nil || h.database.SQL == nil {
		h.renderPage(w, r, http.StatusServiceUnavailable, "error", h.tr(r, "backup.flash.unavailable"))
		return
	}

	if status, _, msg := parseBackupMultipart(w, r); status != 0 {
		h.renderPage(w, r, status, "error", msg)
		return
	}

	if r.PostFormValue("confirm") != "on" {
		h.renderPage(w, r, http.StatusBadRequest, "error", h.tr(r, "backup.flash.restore_confirm_required"))
		return
	}

	raw, status, _, msg := readUploadedBackupFile(r, h)
	if status != 0 {
		h.renderPage(w, r, status, "error", msg)
		return
	}

	archive, err := backup.Inspect(raw, r.PostFormValue("passphrase"))
	if err != nil {
		errStatus := http.StatusBadRequest
		if errors.Is(err, backup.ErrTooLarge) {
			errStatus = http.StatusRequestEntityTooLarge
		}
		h.renderPage(w, r, errStatus, "error", h.inspectErrorMessage(r, err))
		return
	}

	summary, err := backup.Import(r.Context(), h.database.SQL, archive)
	if err != nil {
		h.renderPage(w, r, http.StatusInternalServerError, "error", h.inspectErrorMessage(r, err))
		return
	}

	if h.scheduler != nil {
		h.scheduler.Refresh(r.Context())
	}

	message := h.tr(r, "backup.flash.restore_ok",
		"servers", summary.Servers,
		"tags", summary.ServerTags,
		"limits", summary.ServerTrafficLimits+summary.TrafficLimitRules,
		"channels", summary.AlertChannels,
		"rules", summary.AlertRules,
		"silences", summary.AlertSilences)
	if !archive.IncludesSecrets {
		message += " " + h.tr(r, "backup.flash.restore_redacted_note")
	}
	if summary.EnvKeys > 0 {
		message += " " + h.tr(r, "backup.flash.restore_env_note", "count", summary.EnvKeys)
	}
	h.renderPage(w, r, http.StatusOK, "success", message)
}

func parseBackupMultipart(w http.ResponseWriter, r *http.Request) (int, string, string) {
	// Skip re-wrapping if CSRF middleware already parsed the multipart body.
	if r.MultipartForm == nil {
		r.Body = http.MaxBytesReader(w, r.Body, backup.MaxArchiveBytes+multipartOverheadBytes)
		if err := r.ParseMultipartForm(backup.MaxArchiveBytes); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return http.StatusRequestEntityTooLarge, "too_large", trRequest(r, "backup.flash.restore_too_large")
			}
			return http.StatusBadRequest, "bad_format", trRequest(r, "backup.flash.restore_bad_format")
		}
	}
	return 0, "", ""
}

func readUploadedBackupFile(r *http.Request, h DiagnosticsHandler) ([]byte, int, string, string) {
	file, _, err := r.FormFile("backup_file")
	if err != nil {
		return nil, http.StatusBadRequest, "no_file", h.tr(r, "backup.flash.restore_no_file")
	}
	defer func() {
		_ = file.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(file, backup.MaxArchiveBytes+1))
	if err != nil {
		return nil, http.StatusBadRequest, "no_file", h.tr(r, "backup.flash.restore_no_file")
	}
	if len(raw) > backup.MaxArchiveBytes {
		return nil, http.StatusRequestEntityTooLarge, "too_large", h.tr(r, "backup.flash.restore_too_large")
	}
	return raw, 0, "", ""
}

// secretEnv returns the sensitive NODEXIA_* values currently configured, used
// only for an opt-in secrets export. Empty values are omitted so the export
// records only what is actually set.
func (h DiagnosticsHandler) secretEnv() map[string]string {
	env := map[string]string{}
	add := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			env[key] = value
		}
	}
	add("NODEXIA_SESSION_SECRET", h.config.Security.SessionSecret)
	add("NODEXIA_AUTH_USERNAME", h.config.Security.AdminUsername)
	add("NODEXIA_AUTH_PASSWORD", h.config.Security.AdminPassword)
	add("NODEXIA_TELEGRAM_BOT_TOKEN", h.config.Notify.TelegramBotToken)
	return env
}

// inspectErrorCode returns a stable machine-readable code for the inspect JSON API.
func inspectErrorCode(err error) string {
	switch {
	case errors.Is(err, backup.ErrPassphraseRequired):
		return "passphrase_required"
	case errors.Is(err, backup.ErrWrongPassphrase):
		return "wrong_passphrase"
	case errors.Is(err, backup.ErrUnsupportedVersion):
		return "unsupported_version"
	case errors.Is(err, backup.ErrTooLarge):
		return "too_large"
	case errors.Is(err, backup.ErrDanglingReference):
		return "dangling_reference"
	case errors.Is(err, backup.ErrBadFormat), errors.Is(err, backup.ErrMalformed):
		return "bad_format"
	default:
		return "invalid"
	}
}

// inspectErrorMessage maps a backup sentinel error to a localized message.
func (h DiagnosticsHandler) inspectErrorMessage(r *http.Request, err error) string {
	switch {
	case errors.Is(err, backup.ErrPassphraseRequired):
		return h.tr(r, "backup.flash.restore_passphrase_required")
	case errors.Is(err, backup.ErrWrongPassphrase):
		return h.tr(r, "backup.flash.restore_wrong_passphrase")
	case errors.Is(err, backup.ErrUnsupportedVersion):
		return h.tr(r, "backup.flash.restore_unsupported_version")
	case errors.Is(err, backup.ErrTooLarge):
		return h.tr(r, "backup.flash.restore_too_large")
	case errors.Is(err, backup.ErrBadFormat), errors.Is(err, backup.ErrMalformed):
		return h.tr(r, "backup.flash.restore_bad_format")
	case errors.Is(err, backup.ErrDanglingReference):
		return h.tr(r, "backup.flash.restore_invalid")
	default:
		return h.tr(r, "backup.flash.restore_failed", "error", err.Error())
	}
}

func trRequest(r *http.Request, key string, args ...any) string {
	loc := i18n.FromContext(r.Context())
	if loc == nil {
		loc = i18n.MustDefault().Localizer(i18n.DefaultLanguage)
	}
	return loc.T(key, args...)
}

// tr resolves an i18n key against the request's active locale.
func (h DiagnosticsHandler) tr(r *http.Request, key string, args ...any) string {
	return trRequest(r, key, args...)
}

func writeBackupJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
