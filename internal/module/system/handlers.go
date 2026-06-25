package system

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type PageHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	factRepo   Repository
}

type RefreshHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	factRepo   Repository
}

type FormInput struct {
	Password       string
	PrivateKey     string
	KeyPassphrase  string
	ConnectTimeout string
	CommandTimeout string
}

type ValidationErrors map[string]string

func NewPageHandler(deps module.Dependencies, serverRepo servers.Repository, factRepo Repository) PageHandler {
	return PageHandler{deps: deps, serverRepo: serverRepo, factRepo: factRepo}
}

func NewRefreshHandler(deps module.Dependencies, serverRepo servers.Repository, factRepo Repository) RefreshHandler {
	return RefreshHandler{deps: deps, serverRepo: serverRepo, factRepo: factRepo}
}

func (h PageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	wantRefresh := strings.TrimSpace(r.URL.Query().Get("refresh")) == "1"
	shouldCollect := hasStoredCreds && wantRefresh

	if hasStoredCreds && !wantRefresh {
		hasStored, _ := h.factRepo.HasAny(r.Context(), server.ID)
		if !hasStored {
			shouldCollect = true
		}
	}

	if shouldCollect {
		h.collectAndRender(w, r, server)
		return
	}

	snapshot := view.SystemSnapshotView{}
	if latest, err := h.factRepo.GetLatestByServer(r.Context(), server.ID); err == nil {
		snapshot = snapshotViewFromModel(latest)
	} else if !errors.Is(err, ErrNotFound) {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load system facts", "The latest stored system facts could not be loaded.")
		return
	}

	renderPage(w, r, h.deps, http.StatusOK, server, defaultFormView(h.deps, server, hasStoredCreds), snapshot, view.SystemCollectionResultView{}, "", "")
}

func (h PageHandler) collectAndRender(w http.ResponseWriter, r *http.Request, server servers.Server) {
	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
	connectTimeout := h.deps.Config.SSH.ConnectTimeout
	commandTimeout := h.deps.Config.SSH.CommandTimeout

	connReq := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: connectTimeout,
	}

	snapshot, result, err := Collect(r.Context(), h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    commandTimeout,
	})

	collectionResult := view.SystemCollectionResultView{
		Available:   true,
		Command:     "system fact collector",
		Duration:    formatDuration(result.Duration),
		CollectedAt: formatTimestamp(result.CompletedAt),
		Stdout:      result.Stdout,
		Stderr:      result.Stderr,
	}

	if err != nil {
		collectionResult.Error = err.Error()
		form := defaultFormView(h.deps, server, true)
		renderPage(w, r, h.deps, http.StatusBadGateway, server, form, view.SystemSnapshotView{}, collectionResult, "error", "System fact collection failed.")
		return
	}

	snapshot.ServerID = server.ID
	stored, storeErr := h.factRepo.Append(r.Context(), snapshot)
	if storeErr != nil {
		httperrors.RenderPage(w, r, h.deps, storeErr, "/servers", "Could not persist system facts", "System facts were collected but could not be stored.")
		return
	}

	renderPage(
		w,
		r,
		h.deps,
		http.StatusOK,
		server,
		defaultFormView(h.deps, server, true),
		snapshotViewFromModel(stored),
		collectionResult,
		"success",
		"System facts were collected and stored successfully.",
	)
}

func (h RefreshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	if servers.HasStoredCredentials(server) {
		http.Redirect(w, r, systemURL(server.ID)+"?refresh=1", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid system request", "The submitted system information request could not be parsed.")
		return
	}

	form := formInputFromRequest(r, h.deps)
	validationErrors, connectTimeout, commandTimeout := validateForm(form, server, h.deps)
	if validationErrors.HasAny() {
		hasStored := servers.HasStoredCredentials(server)
		renderPage(
			w,
			r,
			h.deps,
			http.StatusUnprocessableEntity,
			server,
			formViewFromInput(form, validationErrors, server.ID, hasStored),
			view.SystemSnapshotView{},
			view.SystemCollectionResultView{},
			"error",
			"Please fix the highlighted fields before refreshing system facts.",
		)
		return
	}

	snapshot, result, err := Collect(r.Context(), h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: sshclient.ConnectionRequest{
			Host:           server.Host,
			Port:           server.Port,
			Username:       server.Username,
			AuthMode:       server.AuthMode,
			Password:       form.Password,
			PrivateKeyPEM:  form.PrivateKey,
			KeyPassphrase:  form.KeyPassphrase,
			ConnectTimeout: connectTimeout,
		},
		CommandTimeout: commandTimeout,
	})

	collectionResult := view.SystemCollectionResultView{
		Available:   true,
		Command:     "system fact collector",
		Duration:    formatDuration(result.Duration),
		CollectedAt: formatTimestamp(result.CompletedAt),
		Stdout:      result.Stdout,
		Stderr:      result.Stderr,
	}

	if err != nil {
		collectionResult.Error = err.Error()
		renderPage(
			w,
			r,
			h.deps,
			http.StatusBadGateway,
			server,
			defaultFormView(h.deps, server, servers.HasStoredCredentials(server)),
			view.SystemSnapshotView{},
			collectionResult,
			"error",
			"System fact collection failed.",
		)
		return
	}

	snapshot.ServerID = server.ID
	stored, err := h.factRepo.Append(r.Context(), snapshot)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not persist system facts", "System facts were collected but could not be stored.")
		return
	}

	hasStored := servers.HasStoredCredentials(server)
	renderPage(
		w,
		r,
		h.deps,
		http.StatusOK,
		server,
		defaultFormView(h.deps, server, hasStored),
		snapshotViewFromModel(stored),
		collectionResult,
		"success",
		"System facts were collected and stored successfully.",
	)
}

func loadServer(w http.ResponseWriter, r *http.Request, deps module.Dependencies, serverRepo servers.Repository) (servers.Server, bool) {
	serverID, ok := pathID(r)
	if !ok {
		httperrors.RenderPage(w, r, deps, servers.ErrNotFound, "/servers", "Server not found", "The requested server record does not exist.")
		return servers.Server{}, false
	}

	server, err := serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, deps, err, "/servers", "Could not load server", "The system information page could not load the selected server.")
		return servers.Server{}, false
	}

	return server, true
}

func renderPage(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	statusCode int,
	server servers.Server,
	form view.SystemFormView,
	snapshot view.SystemSnapshotView,
	collection view.SystemCollectionResultView,
	flashKind string,
	flashMessage string,
) {
	page := view.NewPageData(deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("system.title")
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-system"
	page.PageTitle = page.T("system.page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("system.page_description")
	if deps.Database != nil {
		page.MigrationCount = deps.Database.MigrationCount()
	}
	page.SystemTarget = view.SystemTargetView{
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
	page.SystemForm = form
	page.SystemFacts = snapshot
	page.SystemCollection = collection
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage
	page.PageStyles = []string{"/static/system.css"}

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render system page", http.StatusInternalServerError)
	}
}

func defaultFormView(deps module.Dependencies, server servers.Server, hasStoredCreds bool) view.SystemFormView {
	return view.SystemFormView{
		Action:                     "/servers/" + formatID(server.ID) + "/system",
		ConnectTimeout:             deps.Config.SSH.ConnectTimeout.String(),
		CommandTimeout:             deps.Config.SSH.CommandTimeout.String(),
		StoredCredentialsAvailable: hasStoredCreds,
		RefreshURL:                 systemURL(server.ID),
		Errors:                     map[string]string{},
	}
}

func systemURL(serverID int64) string {
	return "/servers/" + formatID(serverID) + "/system"
}

func formInputFromRequest(r *http.Request, deps module.Dependencies) FormInput {
	return FormInput{
		Password:       r.FormValue("password"),
		PrivateKey:     r.FormValue("private_key"),
		KeyPassphrase:  r.FormValue("key_passphrase"),
		ConnectTimeout: fallbackString(strings.TrimSpace(r.FormValue("connect_timeout")), deps.Config.SSH.ConnectTimeout.String()),
		CommandTimeout: fallbackString(strings.TrimSpace(r.FormValue("command_timeout")), deps.Config.SSH.CommandTimeout.String()),
	}
}

func formViewFromInput(input FormInput, validationErrors ValidationErrors, serverID int64, hasStoredCreds bool) view.SystemFormView {
	return view.SystemFormView{
		Action:                     "/servers/" + formatID(serverID) + "/system",
		ConnectTimeout:             input.ConnectTimeout,
		CommandTimeout:             input.CommandTimeout,
		StoredCredentialsAvailable: hasStoredCreds,
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

	return validationErrors, connectTimeout, commandTimeout
}

func snapshotViewFromModel(snapshot FactSnapshot) view.SystemSnapshotView {
	osName := fallbackDisplay(snapshot.OSName)
	osVersion := fallbackDisplay(snapshot.OSVersion)
	kernelVersion := fallbackDisplay(snapshot.KernelVersion)
	arch := fallbackDisplay(snapshot.Architecture)

	return view.SystemSnapshotView{
		Available:            true,
		Hostname:             fallbackDisplay(snapshot.Hostname),
		OSName:               osName,
		OSVersion:            osVersion,
		KernelVersion:        kernelVersion,
		Architecture:         arch,
		UptimeHuman:          formatUptime(snapshot.UptimeSeconds),
		UptimeSeconds:        strconv.FormatInt(snapshot.UptimeSeconds, 10),
		LastUpdateAt:         formatUnixTimestamp(snapshot.LastUpdateUnix),
		LastUpdateUnix:       strconv.FormatInt(snapshot.LastUpdateUnix, 10),
		CollectedAt:          formatTimestamp(snapshot.CollectedAt),
		OS:                   osName,
		Platform:             osName,
		PlatformFamily:       "-",
		PlatformVersion:      osVersion,
		KernelArch:           arch,
		VirtualizationSystem: "-",
		VirtualizationRole:   "-",
		CPUModel:             fallbackDisplay(snapshot.CPUModel),
		CPUCores:             formatCount(snapshot.CPUCores),
		TotalRAM:             formatKiB(snapshot.MemTotalKB),
		TotalDisk:            formatKiB(snapshot.DiskTotalKB),
	}
}

// formatCount renders a non-negative count, or "-" when it is zero/unknown.
func formatCount(n int64) string {
	if n <= 0 {
		return "-"
	}
	return strconv.FormatInt(n, 10)
}

func pathID(r *http.Request) (int64, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
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

func fallbackString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func fallbackDisplay(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func (v ValidationErrors) HasAny() bool {
	return len(v) > 0
}
