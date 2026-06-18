package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/backup"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

// maxBackupUpload caps the restore upload size. Backups are logical JSON
// documents of configuration rows — kilobytes in practice — so a generous few
// megabytes both fits any real panel and bounds the work a malicious upload can
// force us to buffer and parse.
const maxBackupUpload = 8 << 20 // 8 MiB

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

// BackupImport handles POST /ops/backup/import. It validates the uploaded file,
// requires an explicit overwrite confirmation, and applies the restore inside a
// single transaction. The result (or a precise error) is shown as a flash on
// the diagnostics page.
func (h DiagnosticsHandler) BackupImport(w http.ResponseWriter, r *http.Request) {
	if h.database == nil || h.database.SQL == nil {
		h.renderPage(w, r, http.StatusServiceUnavailable, "error", h.tr(r, "backup.flash.unavailable"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBackupUpload)
	if err := r.ParseMultipartForm(maxBackupUpload); err != nil {
		h.renderPage(w, r, http.StatusBadRequest, "error", h.tr(r, "backup.flash.restore_too_large"))
		return
	}

	if r.PostFormValue("confirm") != "on" {
		h.renderPage(w, r, http.StatusBadRequest, "error", h.tr(r, "backup.flash.restore_confirm_required"))
		return
	}

	file, _, err := r.FormFile("backup_file")
	if err != nil {
		h.renderPage(w, r, http.StatusBadRequest, "error", h.tr(r, "backup.flash.restore_no_file"))
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxBackupUpload))
	if err != nil {
		h.renderPage(w, r, http.StatusBadRequest, "error", h.tr(r, "backup.flash.restore_no_file"))
		return
	}

	archive, err := backup.Inspect(raw, r.PostFormValue("passphrase"))
	if err != nil {
		h.renderPage(w, r, http.StatusBadRequest, "error", h.inspectErrorMessage(r, err))
		return
	}

	summary, err := backup.Import(r.Context(), h.database.SQL, archive)
	if err != nil {
		h.renderPage(w, r, http.StatusInternalServerError, "error", h.inspectErrorMessage(r, err))
		return
	}

	message := h.tr(r, "backup.flash.restore_ok",
		"servers", summary.Servers,
		"channels", summary.AlertChannels,
		"rules", summary.AlertRules,
		"silences", summary.AlertSilences)
	if summary.EnvKeys > 0 {
		message += " " + h.tr(r, "backup.flash.restore_env_note", "count", summary.EnvKeys)
	}
	h.renderPage(w, r, http.StatusOK, "success", message)
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

// inspectErrorMessage maps a backup sentinel error to a localized message.
func (h DiagnosticsHandler) inspectErrorMessage(r *http.Request, err error) string {
	switch {
	case errors.Is(err, backup.ErrPassphraseRequired):
		return h.tr(r, "backup.flash.restore_passphrase_required")
	case errors.Is(err, backup.ErrWrongPassphrase):
		return h.tr(r, "backup.flash.restore_wrong_passphrase")
	case errors.Is(err, backup.ErrUnsupportedVersion):
		return h.tr(r, "backup.flash.restore_unsupported_version")
	case errors.Is(err, backup.ErrBadFormat), errors.Is(err, backup.ErrMalformed):
		return h.tr(r, "backup.flash.restore_bad_format")
	case errors.Is(err, backup.ErrDanglingReference):
		return h.tr(r, "backup.flash.restore_invalid")
	default:
		return h.tr(r, "backup.flash.restore_failed", "error", err.Error())
	}
}

// tr resolves an i18n key against the request's active locale.
func (h DiagnosticsHandler) tr(r *http.Request, key string, args ...any) string {
	loc := i18n.FromContext(r.Context())
	if loc == nil {
		loc = i18n.MustDefault().Localizer(i18n.DefaultLanguage)
	}
	return loc.T(key, args...)
}
