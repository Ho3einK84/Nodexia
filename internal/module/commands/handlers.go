package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

const streamRefreshMillis = 2000

type PageHandler struct {
	deps        module.Dependencies
	serverRepo  servers.Repository
	historyRepo Repository
}

type ActionHandler struct {
	deps        module.Dependencies
	serverRepo  servers.Repository
	historyRepo Repository
}

type FormInput struct {
	Intent         string
	Command        string
	Password       string
	PrivateKey     string
	KeyPassphrase  string
	ConnectTimeout string
	CommandTimeout string
}

type ValidationErrors map[string]string

type commandPreset struct {
	Key         string
	Label       string
	Description string
	Command     string
}

var defaultPresets = []commandPreset{
	{
		Key:         "system-overview",
		Label:       "System overview",
		Description: "Kernel, uptime, logged-in user, and hostname in one pass.",
		Command:     "echo HOST=$(hostname) && echo USER=$(whoami) && uname -a && uptime",
	},
	{
		Key:         "disk-memory",
		Label:       "Disk and memory",
		Description: "Quick capacity snapshot for filesystem and memory pressure.",
		Command:     "df -h && echo && free -h",
	},
	{
		Key:         "top-processes",
		Label:       "Top processes",
		Description: "Top CPU consumers without leaving the panel.",
		Command:     "ps -eo pid,ppid,cmd,%mem,%cpu --sort=-%cpu | head -n 15",
	},
	{
		Key:         "network-listeners",
		Label:       "Listening ports",
		Description: "Inspect open TCP and UDP listeners on the host.",
		Command:     "ss -tulpn",
	},
	{
		Key:         "install-vnstat",
		Label:       "Install vnstat",
		Description: "Install the vnstat network traffic monitor. Requires root or sudo access.",
		Command:     "apt install -y vnstat",
	},
}

func NewPageHandler(deps module.Dependencies, serverRepo servers.Repository, historyRepo Repository) PageHandler {
	return PageHandler{
		deps:        deps,
		serverRepo:  serverRepo,
		historyRepo: historyRepo,
	}
}

func NewActionHandler(deps module.Dependencies, serverRepo servers.Repository, historyRepo Repository) ActionHandler {
	return ActionHandler{
		deps:        deps,
		serverRepo:  serverRepo,
		historyRepo: historyRepo,
	}
}

func (h PageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, history, ok := h.loadServerAndHistory(w, r)
	if !ok {
		return
	}

	form := defaultFormInput(h.deps)
	form.StoredCredentialsAvailable = servers.HasStoredCredentials(server)
	if preset, ok := presetByKey(strings.TrimSpace(r.URL.Query().Get("preset"))); ok {
		form.Command = preset.Command
	}

	streamView, flashKind, flashMessage := h.loadStreamView(r, server.ID)
	renderPage(
		w,
		r,
		h.deps,
		http.StatusOK,
		server,
		history,
		form,
		presetViews(server.ID),
		view.CommandResultView{},
		view.ConnectionTestView{},
		streamView,
		flashKind,
		flashMessage,
	)
}

func (h ActionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		httperrors.RenderPage(w, r, h.deps, servers.ErrNotFound, "/servers", "Server not found", "The requested server record does not exist.")
		return
	}

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid command request", "The submitted remote action could not be parsed.")
		return
	}

	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load server", "The command center could not load the selected server.")
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	form := formInputFromRequest(r, h.deps)
	if hasStoredCreds {
		password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
		if strings.TrimSpace(form.Password) == "" {
			form.Password = password
		}
		if strings.TrimSpace(form.PrivateKey) == "" {
			form.PrivateKey = privateKey
		}
		if strings.TrimSpace(form.KeyPassphrase) == "" {
			form.KeyPassphrase = keyPassphrase
		}
	}
	// "Run in terminal" submits — and interactive/TUI commands (top, vim,
	// ssh, mysql, …) that would hang the non-interactive runner — go to the
	// in-browser terminal, which runs them in a real PTY.  This happens
	// before credential validation: the terminal collects its own credentials.
	command := strings.TrimSpace(form.Command)
	if command != "" &&
		(form.Intent == "terminal" ||
			((form.Intent == "run" || form.Intent == "stream") && isInteractiveCommand(command))) {
		http.Redirect(w, r, terminalRunURL(server.ID, command), http.StatusSeeOther)
		return
	}

	validationErrors, connectTimeout, commandTimeout := validateForm(form, server, h.deps)
	if validationErrors.HasAny() {
		history, historyErr := h.historyRepo.ListByServer(r.Context(), serverID, defaultHistoryLimit)
		if historyErr != nil {
			httperrors.RenderPage(w, r, h.deps, historyErr, "/servers", "Could not load command history", "The command center could not load recent command history.")
			return
		}

		renderPage(
			w,
			r,
			h.deps,
			http.StatusUnprocessableEntity,
			server,
			history,
			formViewFromInput(form, validationErrors, hasStoredCreds),
			presetViews(server.ID),
			view.CommandResultView{},
			view.ConnectionTestView{},
			view.CommandStreamView{},
			"error",
			"Please fix the highlighted fields before submitting the remote action.",
		)
		return
	}

	request := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       form.Password,
		PrivateKeyPEM:  form.PrivateKey,
		KeyPassphrase:  form.KeyPassphrase,
		ConnectTimeout: connectTimeout,
	}

	switch form.Intent {
	case "run", "stream":
		// All command execution goes through the background stream session:
		// the POST redirects immediately and the live page polls for output,
		// so a slow command (apt upgrade, …) can never outlive the server's
		// write timeout or the reverse proxy and surface as a 502.
		h.handleStreamStart(w, r, server, request, form, commandTimeout)
		return
	case "test":
	default:
		history, historyErr := h.historyRepo.ListByServer(r.Context(), serverID, defaultHistoryLimit)
		if historyErr != nil {
			httperrors.RenderPage(w, r, h.deps, historyErr, "/servers", "Could not load command history", "The command center could not load recent command history.")
			return
		}

		formErrors := ValidationErrors{"intent": "Choose a valid remote action."}
		renderPage(
			w,
			r,
			h.deps,
			http.StatusUnprocessableEntity,
			server,
			history,
			formViewFromInput(form, formErrors, hasStoredCreds),
			presetViews(server.ID),
			view.CommandResultView{},
			view.ConnectionTestView{},
			view.CommandStreamView{},
			"error",
			"Choose a valid remote action and submit the form again.",
		)
		return
	}

	// SSH connection test: synchronous on purpose — it is bounded by the
	// connect timeout, which is far below the server's write timeout.
	var (
		connectionResult view.ConnectionTestView
		flashKind        string
		flashMessage     string
		statusCode       = http.StatusOK
	)

	result, err := h.deps.SSH.TestConnection(r.Context(), request)
	if err != nil {
		statusCode = http.StatusBadGateway
		connectionResult = view.ConnectionTestView{
			Available: true,
			Duration:  formatDuration(result.Duration),
			Error:     err.Error(),
		}
		flashKind = "error"
		flashMessage = "SSH connection test failed."
	} else {
		connectionResult = view.ConnectionTestView{
			Available: true,
			Duration:  formatDuration(result.Duration),
			Message:   "Connected to " + result.RemoteAddress + " successfully.",
		}
		flashKind = "success"
		flashMessage = "SSH connection test completed successfully."
	}

	history, err := h.historyRepo.ListByServer(r.Context(), serverID, defaultHistoryLimit)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load command history", "The command center could not load recent command history.")
		return
	}

	nextForm := defaultFormInput(h.deps)
	nextForm.StoredCredentialsAvailable = hasStoredCreds
	renderPage(
		w,
		r,
		h.deps,
		statusCode,
		server,
		history,
		nextForm,
		presetViews(server.ID),
		view.CommandResultView{},
		connectionResult,
		view.CommandStreamView{},
		flashKind,
		flashMessage,
	)
}

func (h ActionHandler) handleStreamStart(
	w http.ResponseWriter,
	r *http.Request,
	server servers.Server,
	request sshclient.ConnectionRequest,
	form FormInput,
	commandTimeout time.Duration,
) {
	if h.deps.CommandStreams == nil {
		httperrors.RenderPage(w, r, h.deps, errors.New("missing command stream store"), "/servers", "Streaming unavailable", "The command stream store is not configured yet.")
		return
	}

	command := strings.TrimSpace(form.Command)
	session := h.deps.CommandStreams.Create(server.ID, command)
	go h.runStreamSession(session.ID, server.ID, command, sshclient.CommandRequest{
		ConnectionRequest: request,
		Command:           command,
		CommandTimeout:    commandTimeout,
	})

	http.Redirect(w, r, commandPageURL(server.ID, session.ID), http.StatusSeeOther)
}

func (h ActionHandler) runStreamSession(sessionID string, serverID int64, command string, request sshclient.CommandRequest) {
	ctx := context.Background()
	result, runErr := h.deps.SSH.StreamCommand(ctx, request, sshclient.StreamHandlers{
		OnStdout: func(chunk string) {
			h.deps.CommandStreams.AppendStdout(sessionID, chunk)
		},
		OnStderr: func(chunk string) {
			h.deps.CommandStreams.AppendStderr(sessionID, chunk)
		},
	})

	historyEntry := HistoryEntry{
		ServerID:   serverID,
		Command:    command,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		ExecutedAt: result.CompletedAt,
	}
	if runErr != nil && strings.TrimSpace(historyEntry.Stderr) == "" {
		historyEntry.Stderr = runErr.Error()
	}

	historyID := int64(0)
	if appended, appendErr := h.historyRepo.Append(ctx, historyEntry); appendErr == nil {
		historyID = appended.ID
	} else if runErr == nil {
		runErr = fmt.Errorf("persist stream history: %w", appendErr)
	} else {
		runErr = errors.Join(runErr, fmt.Errorf("persist stream history: %w", appendErr))
	}

	if runErr != nil {
		h.deps.CommandStreams.Fail(sessionID, result.ExitCode, result.CompletedAt, runErr, historyID)
		return
	}

	h.deps.CommandStreams.Complete(sessionID, result.ExitCode, result.CompletedAt, historyID)
}

// StreamEventsHandler serves GET /servers/{id}/commands/stream/{stream}/events:
// the Server-Sent Events feed a running command's live page subscribes to. It
// validates that the stream belongs to the server in the URL, then hands off to
// the store's SSE streamer.
type StreamEventsHandler struct {
	deps module.Dependencies
}

func NewStreamEventsHandler(deps module.Dependencies) StreamEventsHandler {
	return StreamEventsHandler{deps: deps}
}

func (h StreamEventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if h.deps.CommandStreams == nil {
		http.Error(w, "live stream store unavailable", http.StatusServiceUnavailable)
		return
	}

	streamID := strings.TrimSpace(r.PathValue("stream"))
	snapshot, found := h.deps.CommandStreams.Get(streamID)
	if !found || snapshot.ServerID != serverID {
		http.Error(w, "live stream not found", http.StatusNotFound)
		return
	}

	h.deps.CommandStreams.ServeSSE(w, r, streamID)
}

func (h PageHandler) loadServerAndHistory(w http.ResponseWriter, r *http.Request) (servers.Server, []HistoryEntry, bool) {
	serverID, ok := pathID(r)
	if !ok {
		httperrors.RenderPage(w, r, h.deps, servers.ErrNotFound, "/servers", "Server not found", "The requested server record does not exist.")
		return servers.Server{}, nil, false
	}

	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load server", "The command center could not load the selected server.")
		return servers.Server{}, nil, false
	}

	history, err := h.historyRepo.ListByServer(r.Context(), serverID, defaultHistoryLimit)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load command history", "The command center could not load recent command history.")
		return servers.Server{}, nil, false
	}

	return server, history, true
}

func (h PageHandler) loadStreamView(r *http.Request, serverID int64) (view.CommandStreamView, string, string) {
	if h.deps.CommandStreams == nil {
		return view.CommandStreamView{}, "", ""
	}

	streamID := strings.TrimSpace(r.URL.Query().Get("stream"))
	if streamID == "" {
		return view.CommandStreamView{}, "", ""
	}

	snapshot, ok := h.deps.CommandStreams.Get(streamID)
	if !ok || snapshot.ServerID != serverID {
		return view.CommandStreamView{}, "error", "The requested live stream session is no longer available."
	}

	flashKind := "success"
	flashMessage := "Live output is refreshing automatically while the command is still running."
	if snapshot.Status == commandstream.StatusFailed {
		flashKind = "error"
		flashMessage = "The live command session ended with an error."
	} else if snapshot.Status == commandstream.StatusCompleted {
		flashMessage = "Live command session completed."
	}

	return commandStreamView(serverID, snapshot), flashKind, flashMessage
}

func renderPage(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	statusCode int,
	server servers.Server,
	history []HistoryEntry,
	form view.CommandFormView,
	presets []view.CommandPresetView,
	commandResult view.CommandResultView,
	connectionResult view.ConnectionTestView,
	streamView view.CommandStreamView,
	flashKind string,
	flashMessage string,
) {
	page := view.NewPageData(deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("commands.title")
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-commands"
	page.PageTitle = page.T("commands.page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("commands.page_description")
	if deps.Database != nil {
		page.MigrationCount = deps.Database.MigrationCount()
	}
	page.CommandTarget = view.CommandTargetView{
		ID:                 server.ID,
		Name:               server.Name,
		Host:               server.Host,
		Port:               server.Port,
		AuthMode:           server.AuthMode,
		Username:           server.Username,
		Tags:               server.Tags,
		CredentialStrategy: server.CredentialStrategy,
		CredentialRef:      server.CredentialRef,
		UpdatedAt:          formatTimestamp(server.UpdatedAt),
	}
	if form.Action == "" {
		form.Action = "/servers/" + formatID(server.ID) + "/commands"
	}
	form.InteractivePrograms = interactiveProgramsAttr()
	page.CommandForm = form
	page.CommandPresets = presets
	page.CommandResult = commandResult
	page.ConnectionResult = connectionResult
	page.CommandStream = streamView
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage
	page.PageStyles = []string{"/static/commands.css"}
	page.PageScripts = []string{"/static/commands.js"}

	items := make([]view.CommandHistoryView, 0, len(history))
	for _, entry := range history {
		items = append(items, view.CommandHistoryView{
			ID:         entry.ID,
			Command:    entry.Command,
			ExitCode:   formatExitCode(entry.ExitCode),
			Stdout:     entry.Stdout,
			Stderr:     entry.Stderr,
			ExecutedAt: formatTimestamp(entry.ExecutedAt),
		})
	}
	page.CommandHistory = items

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render command center page", http.StatusInternalServerError)
	}
}

func pathID(r *http.Request) (int64, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}

	return id, true
}

func defaultFormInput(deps module.Dependencies) view.CommandFormView {
	return view.CommandFormView{
		Action:         "",
		Intent:         "",
		Command:        "",
		ConnectTimeout: deps.Config.SSH.ConnectTimeout.String(),
		CommandTimeout: deps.Config.SSH.CommandTimeout.String(),
		Errors:         map[string]string{},
	}
}

func commandsURL(serverID int64) string {
	return "/servers/" + formatID(serverID) + "/commands"
}

func formInputFromRequest(r *http.Request, deps module.Dependencies) FormInput {
	return FormInput{
		Intent:         strings.TrimSpace(r.FormValue("intent")),
		Command:        r.FormValue("command"),
		Password:       r.FormValue("password"),
		PrivateKey:     r.FormValue("private_key"),
		KeyPassphrase:  r.FormValue("key_passphrase"),
		ConnectTimeout: fallbackString(strings.TrimSpace(r.FormValue("connect_timeout")), deps.Config.SSH.ConnectTimeout.String()),
		CommandTimeout: fallbackString(strings.TrimSpace(r.FormValue("command_timeout")), deps.Config.SSH.CommandTimeout.String()),
	}
}

func formViewFromInput(input FormInput, validationErrors ValidationErrors, hasStoredCreds ...bool) view.CommandFormView {
	stored := len(hasStoredCreds) > 0 && hasStoredCreds[0]
	return view.CommandFormView{
		Intent:                     input.Intent,
		Command:                    strings.TrimSpace(input.Command),
		ConnectTimeout:             input.ConnectTimeout,
		CommandTimeout:             input.CommandTimeout,
		StoredCredentialsAvailable: stored,
		Errors:                     validationErrors,
	}
}

func validateForm(input FormInput, server servers.Server, deps module.Dependencies) (ValidationErrors, time.Duration, time.Duration) {
	validationErrors := ValidationErrors{}

	connectTimeout, err := parseDurationField(input.ConnectTimeout, deps.Config.SSH.ConnectTimeout)
	if err != nil {
		validationErrors["connect_timeout"] = "Enter a valid connection timeout such as 10s or 30s."
	}

	commandTimeout, err := parseDurationField(input.CommandTimeout, deps.Config.SSH.CommandTimeout)
	if err != nil {
		validationErrors["command_timeout"] = "Enter a valid command timeout such as 20s or 1m."
	}

	switch input.Intent {
	case "test":
	case "run", "stream", "terminal":
		command := strings.TrimSpace(input.Command)
		if command == "" {
			validationErrors["command"] = "Enter a shell command to execute."
		} else {
			if strings.ContainsRune(command, '\x00') {
				validationErrors["command"] = "Command must not contain null bytes."
			}
			if len(command) > 4000 {
				validationErrors["command"] = "Command must be 4000 characters or fewer."
			}
		}
	default:
		validationErrors["intent"] = "Choose a valid remote action."
	}

	// The terminal page collects its own credentials, so a "Run in terminal"
	// submit does not require them here.
	if input.Intent != "terminal" {
		password := strings.TrimSpace(input.Password)
		privateKey := strings.TrimSpace(input.PrivateKey)
		switch server.AuthMode {
		case "password":
			if password == "" {
				validationErrors["password"] = "Enter the SSH password for this runtime session."
			}
		case "key":
			if privateKey == "" {
				validationErrors["private_key"] = "Paste the SSH private key for this runtime session."
			}
		case "hybrid":
			if password == "" && privateKey == "" {
				validationErrors["password"] = "Provide a password or private key for hybrid authentication."
				validationErrors["private_key"] = "Provide a private key or password for hybrid authentication."
			}
		default:
			if password == "" && privateKey == "" {
				validationErrors["password"] = "Provide runtime SSH credentials before continuing."
			}
		}
	}

	return validationErrors, connectTimeout, commandTimeout
}

func presetViews(serverID int64) []view.CommandPresetView {
	items := make([]view.CommandPresetView, 0, len(defaultPresets))
	for _, preset := range defaultPresets {
		items = append(items, view.CommandPresetView{
			Key:         preset.Key,
			Label:       preset.Label,
			Description: preset.Description,
			Command:     preset.Command,
			Href:        fmt.Sprintf("/servers/%s/commands?preset=%s", formatID(serverID), url.QueryEscape(preset.Key)),
		})
	}
	return items
}

func presetByKey(key string) (commandPreset, bool) {
	for _, preset := range defaultPresets {
		if preset.Key == key {
			return preset, true
		}
	}
	return commandPreset{}, false
}

func commandStreamView(serverID int64, snapshot commandstream.Snapshot) view.CommandStreamView {
	durationEnd := snapshot.CompletedAt
	if snapshot.Status == commandstream.StatusRunning {
		durationEnd = time.Now().UTC()
	}

	return view.CommandStreamView{
		Available:     true,
		ID:            snapshot.ID,
		Status:        snapshot.Status,
		IsRunning:     snapshot.Status == commandstream.StatusRunning,
		Command:       snapshot.Command,
		ExitCode:      formatExitCode(snapshot.ExitCode),
		StartedAt:     formatTimestamp(snapshot.StartedAt),
		UpdatedAt:     formatTimestamp(snapshot.UpdatedAt),
		CompletedAt:   formatTimestamp(snapshot.CompletedAt),
		Duration:      formatDuration(durationEnd.Sub(snapshot.StartedAt)),
		Stdout:        snapshot.Stdout,
		Stderr:        snapshot.Stderr,
		Error:         snapshot.Error,
		HistoryID:     snapshot.HistoryID,
		RefreshURL:    commandPageURL(serverID, snapshot.ID),
		RefreshMillis: streamRefreshMillis,
	}
}

func commandPageURL(serverID int64, streamID string) string {
	return fmt.Sprintf("/servers/%s/commands?stream=%s", formatID(serverID), url.QueryEscape(streamID))
}

// terminalRunURL points at the interactive terminal with an init command that
// auto-runs once the shell connects.
func terminalRunURL(serverID int64, command string) string {
	return fmt.Sprintf("/servers/%s/terminal?init=%s", formatID(serverID), url.QueryEscape(strings.TrimSpace(command)))
}

func (v ValidationErrors) HasAny() bool {
	return len(v) > 0
}

func parseDurationField(value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}

	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed, nil
	}

	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, nil
	}

	return 0, fmt.Errorf("invalid duration %q", value)
}

func fallbackString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return "-"
	}
	return value.Round(time.Millisecond).String()
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatExitCode(value *int) string {
	if value == nil {
		return "n/a"
	}
	return strconv.Itoa(*value)
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}
