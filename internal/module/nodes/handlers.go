package nodes

import (
	"context"
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
	repo       Repository
	detectors  []Detector
}

type RefreshHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	repo       Repository
	detectors  []Detector
}

type FormInput struct {
	Password       string
	PrivateKey     string
	KeyPassphrase  string
	ConnectTimeout string
	CommandTimeout string
}

type ValidationErrors map[string]string

func NewPageHandler(deps module.Dependencies, serverRepo servers.Repository, repo Repository, detectors []Detector) PageHandler {
	return PageHandler{deps: deps, serverRepo: serverRepo, repo: repo, detectors: detectors}
}

func NewRefreshHandler(deps module.Dependencies, serverRepo servers.Repository, repo Repository, detectors []Detector) RefreshHandler {
	return RefreshHandler{deps: deps, serverRepo: serverRepo, repo: repo, detectors: detectors}
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
		hasStored, _ := h.repo.HasAny(r.Context(), server.ID)
		if !hasStored {
			shouldCollect = true
		}
	}

	if shouldCollect {
		h.collectAndRender(w, r, server)
		return
	}

	snapshots := make([]view.NodeSnapshotView, 0)
	if latest, err := h.repo.GetLatestByServer(r.Context(), server.ID); err == nil {
		for _, snapshot := range latest {
			snapshots = append(snapshots, snapshotViewFromModel(snapshot))
		}
	} else if !errors.Is(err, ErrNotFound) {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load node snapshots", "The latest node discovery snapshot could not be loaded.")
		return
	}

	renderPage(w, r, h.deps, http.StatusOK, server, defaultFormView(h.deps, server, hasStoredCreds), snapshots, view.NodeCollectionResultView{}, "", "")
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

	collectCtx, cancelCollect := boundedCollectCtx(r.Context(), h.deps)
	defer cancelCollect()

	snapshots, probes, err := Collect(collectCtx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    commandTimeout,
	}, h.detectors)
	if err != nil {
		renderPage(
			w,
			r,
			h.deps,
			http.StatusBadGateway,
			server,
			defaultFormView(h.deps, server, true),
			nil,
			view.NodeCollectionResultView{
				Available: true,
				Error:     err.Error(),
			},
			"error",
			"Node discovery failed.",
		)
		return
	}

	collectedAt := latestCollectedAt(snapshots, probes)
	if err := h.repo.ReplaceLatest(r.Context(), server.ID, snapshots, collectedAt); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not persist node snapshots", "Node discovery ran but the latest snapshot could not be stored.")
		return
	}

	stored, err := h.repo.GetLatestByServer(r.Context(), server.ID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not reload node snapshots", "Node discovery completed but the latest snapshot could not be reloaded.")
		return
	}

	snapshotViews := make([]view.NodeSnapshotView, 0, len(stored))
	for _, snapshot := range stored {
		snapshotViews = append(snapshotViews, snapshotViewFromModel(snapshot))
	}

	collectionView := collectionViewFromProbes(probes, collectedAt)
	flashMessage := "Node discovery evidence was collected and stored successfully."
	if len(stored) == 1 && stored[0].NodeType == "none" {
		flashMessage = "Node discovery finished, but no generic detector matched the collected evidence yet."
	}

	renderPage(
		w,
		r,
		h.deps,
		http.StatusOK,
		server,
		defaultFormView(h.deps, server, true),
		snapshotViews,
		collectionView,
		"success",
		flashMessage,
	)
}

func (h RefreshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	if servers.HasStoredCredentials(server) {
		http.Redirect(w, r, nodesURL(server.ID)+"?refresh=1", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid node discovery request", "The submitted node discovery request could not be parsed.")
		return
	}

	form := formInputFromRequest(r, h.deps)
	validationErrors, connectTimeout, commandTimeout := validateForm(form, server, h.deps)
	if validationErrors.HasAny() {
		renderPage(
			w,
			r,
			h.deps,
			http.StatusUnprocessableEntity,
			server,
			formViewFromInput(form, validationErrors, server.ID),
			nil,
			view.NodeCollectionResultView{},
			"error",
			"Please fix the highlighted fields before refreshing node discovery.",
		)
		return
	}

	collectCtx, cancelCollect := boundedCollectCtx(r.Context(), h.deps)
	defer cancelCollect()

	snapshots, probes, err := Collect(collectCtx, h.deps.SSH, sshclient.CommandRequest{
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
	}, h.detectors)
	if err != nil {
		renderPage(
			w,
			r,
			h.deps,
			http.StatusBadGateway,
			server,
			defaultFormView(h.deps, server, servers.HasStoredCredentials(server)),
			nil,
			view.NodeCollectionResultView{
				Available: true,
				Error:     err.Error(),
			},
			"error",
			"Node discovery failed.",
		)
		return
	}

	collectedAt := latestCollectedAt(snapshots, probes)
	if err := h.repo.ReplaceLatest(r.Context(), server.ID, snapshots, collectedAt); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not persist node snapshots", "Node discovery ran but the latest snapshot could not be stored.")
		return
	}

	stored, err := h.repo.GetLatestByServer(r.Context(), server.ID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not reload node snapshots", "Node discovery completed but the latest snapshot could not be reloaded.")
		return
	}

	snapshotViews := make([]view.NodeSnapshotView, 0, len(stored))
	for _, snapshot := range stored {
		snapshotViews = append(snapshotViews, snapshotViewFromModel(snapshot))
	}

	collectionView := collectionViewFromProbes(probes, collectedAt)
	flashMessage := "Node discovery evidence was collected and stored successfully."
	if len(stored) == 1 && stored[0].NodeType == "none" {
		flashMessage = "Node discovery finished, but no generic detector matched the collected evidence yet."
	}

	renderPage(
		w,
		r,
		h.deps,
		http.StatusOK,
		server,
		defaultFormView(h.deps, server, servers.HasStoredCredentials(server)),
		snapshotViews,
		collectionView,
		"success",
		flashMessage,
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
		httperrors.RenderPage(w, r, deps, err, "/servers", "Could not load server", "The node discovery page could not load the selected server.")
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
	form view.NodeFormView,
	snapshots []view.NodeSnapshotView,
	collection view.NodeCollectionResultView,
	flashKind string,
	flashMessage string,
) {
	page := view.NewPageData(deps.Config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Nodes"
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-nodes"
	page.PageTitle = "Node discovery for " + server.Name
	page.PageDescription = "Detect Rebecca and PasarGuard nodes over SSH, classify the likely install mode, and summarize occupied ports, API visibility, and matched runtime evidence."
	if deps.Database != nil {
		page.MigrationCount = deps.Database.MigrationCount()
	}
	page.NodeTarget = view.NodeTargetView{
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
	page.NodeForm = form
	page.NodeSnapshots = snapshots
	page.NodeCollection = collection
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage
	page.PageStyles = []string{"/static/nodes.css"}

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render nodes page", http.StatusInternalServerError)
	}
}

func defaultFormView(deps module.Dependencies, server servers.Server, hasStoredCreds bool) view.NodeFormView {
	return view.NodeFormView{
		Action:                    "/servers/" + formatID(server.ID) + "/nodes",
		ConnectTimeout:            deps.Config.SSH.ConnectTimeout.String(),
		CommandTimeout:            deps.Config.SSH.CommandTimeout.String(),
		StoredCredentialsAvailable: hasStoredCreds,
		RefreshURL:                nodesURL(server.ID),
		Errors:                    map[string]string{},
	}
}

func nodesURL(serverID int64) string {
	return "/servers/" + formatID(serverID) + "/nodes"
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

func formViewFromInput(input FormInput, validationErrors ValidationErrors, serverID int64) view.NodeFormView {
	return view.NodeFormView{
		Action:         "/servers/" + formatID(serverID) + "/nodes",
		ConnectTimeout: input.ConnectTimeout,
		CommandTimeout: input.CommandTimeout,
		Errors:         validationErrors,
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

func snapshotViewFromModel(snapshot Snapshot) view.NodeSnapshotView {
	return view.NodeSnapshotView{
		NodeType:     fallbackDisplay(snapshot.NodeType),
		ServiceName:  fallbackDisplay(snapshot.ServiceName),
		InstallMode:  fallbackDisplay(snapshot.InstallMode),
		Version:      fallbackDisplay(snapshot.Version),
		HealthStatus: fallbackDisplay(snapshot.HealthStatus),
		ActivePorts:  snapshot.ActivePorts,
		XrayPorts:    snapshot.XrayPorts,
		ServicePort:  fallbackDisplay(snapshot.ServicePort),
		APIPort:      fallbackDisplay(snapshot.APIPort),
		Protocol:     fallbackDisplay(snapshot.Protocol),
		Confidence:   fallbackDisplay(snapshot.Confidence),
		Dependencies: snapshot.Dependencies,
		Evidence:     snapshot.Evidence,
		CollectedAt:  formatTimestamp(snapshot.CollectedAt),
	}
}

func collectionViewFromProbes(probes []ProbeReport, collectedAt time.Time) view.NodeCollectionResultView {
	viewProbes := make([]view.NodeProbeView, 0, len(probes))
	var totalDuration time.Duration
	var errorsList []string

	for _, probe := range probes {
		totalDuration += probe.Result.Duration
		probeError := ""
		if probe.Error != nil {
			probeError = probe.Error.Error()
			errorsList = append(errorsList, probe.Label+": "+probeError)
		}
		viewProbes = append(viewProbes, view.NodeProbeView{
			Label:    probe.Label,
			Command:  probe.Command,
			Duration: formatDuration(probe.Result.Duration),
			Stdout:   strings.TrimSpace(probe.Result.Stdout),
			Stderr:   strings.TrimSpace(probe.Result.Stderr),
			Error:    probeError,
		})
	}

	return view.NodeCollectionResultView{
		Available:   true,
		CollectedAt: formatTimestamp(collectedAt),
		Duration:    formatDuration(totalDuration),
		ProbeCount:  len(viewProbes),
		Probes:      viewProbes,
		Error:       strings.Join(errorsList, "\n"),
	}
}

func latestCollectedAt(snapshots []Snapshot, probes []ProbeReport) time.Time {
	for _, snapshot := range snapshots {
		if !snapshot.CollectedAt.IsZero() {
			return snapshot.CollectedAt
		}
	}
	for _, probe := range probes {
		if !probe.Result.CompletedAt.IsZero() {
			return probe.Result.CompletedAt
		}
	}
	return time.Now().UTC()
}

// boundedCollectCtx caps SSH collection so it completes before the HTTP write
// timeout fires. Without this a slow handshake can trigger a "superfluous
// response.WriteHeader" error when the server tears down the connection.
func boundedCollectCtx(ctx context.Context, deps module.Dependencies) (context.Context, context.CancelFunc) {
	writeTimeout := deps.Config.HTTP.WriteTimeout
	if writeTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	budget := writeTimeout - 2*time.Second
	if budget <= 0 {
		budget = writeTimeout / 2
	}
	return context.WithTimeout(ctx, budget)
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
