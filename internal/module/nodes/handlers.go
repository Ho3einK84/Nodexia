package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

const streamRefreshMillis = 2000

// Handlers serves the nodes route group.  All node knowledge (discovery,
// action commands, install support) comes from the configured providers.
type Handlers struct {
	deps                 module.Dependencies
	serverRepo           servers.Repository
	repo                 Repository
	providers            []Provider
	installableProviders []InstallableProvider
	installs             *installStore
}

func NewHandlers(deps module.Dependencies, serverRepo servers.Repository, repo Repository, providers []Provider) *Handlers {
	if len(providers) == 0 {
		providers = DefaultProviders()
	}
	installable := DefaultInstallableProviders()
	return &Handlers{
		deps:                 deps,
		serverRepo:           serverRepo,
		repo:                 repo,
		providers:            providers,
		installableProviders: installable,
		installs:             newInstallStore(),
	}
}

type FormInput struct {
	Password       string
	PrivateKey     string
	KeyPassphrase  string
	ConnectTimeout string
	CommandTimeout string
}

// pageView bundles everything the nodes page template renders.
type pageView struct {
	status          int
	form            view.NodeFormView
	snapshots       []view.NodeSnapshotView
	collection      view.NodeCollectionResultView
	stream          view.CommandStreamView
	deployForm      view.NodeDeployFormView
	openDeployModal bool
	flashKind       string
	flashMessage    string
}

// ── GET /servers/{id}/nodes ───────────────────────────────────────────────────

func (h *Handlers) Page(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	streamID := strings.TrimSpace(r.URL.Query().Get("stream"))
	refreshParam := strings.TrimSpace(r.URL.Query().Get("refresh"))
	wantRefresh := refreshParam == "1"

	// Never kick off an SSH discovery sweep while the page is polling a live
	// action stream — only explicit refreshes or stale/first-ever visit collect.
	shouldCollect := hasStoredCreds && wantRefresh && streamID == ""
	if hasStoredCreds && !wantRefresh && streamID == "" && refreshParam != "0" {
		latest, err := h.repo.GetLatestByServer(r.Context(), server.ID)
		if err != nil || len(latest) == 0 {
			shouldCollect = true
		}
	}

	if shouldCollect {
		h.collectAndRender(w, r, server)
		return
	}

	snapshots, err := h.storedSnapshotViews(r.Context(), server, hasStoredCreds)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load node snapshots", "The latest node discovery snapshot could not be loaded.")
		return
	}

	page := pageView{
		status:     http.StatusOK,
		form:       h.defaultFormView(server, hasStoredCreds),
		snapshots:  snapshots,
		deployForm: h.deployFormView(r, server, hasStoredCreds),
	}
	page.stream, page.flashKind, page.flashMessage = h.loadStreamView(streamID, server.ID)
	if page.flashKind == "" {
		page.flashKind, page.flashMessage = queryFlash(r)
	}

	h.renderPage(w, r, server, page)
}

func (h *Handlers) collectAndRender(w http.ResponseWriter, r *http.Request, server servers.Server) {
	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
	connReq := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
	}
	h.collectWith(w, r, server, connReq, h.deps.Config.SSH.CommandTimeout, true)
}

// ── POST /servers/{id}/nodes (discovery refresh) ──────────────────────────────

func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
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
		h.renderPage(w, r, server, pageView{
			status:       http.StatusUnprocessableEntity,
			form:         formViewFromInput(form, validationErrors, server.ID),
			deployForm:   h.deployFormView(r, server, false),
			flashKind:    "error",
			flashMessage: "Please fix the highlighted fields before refreshing node discovery.",
		})
		return
	}

	connReq := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       form.Password,
		PrivateKeyPEM:  form.PrivateKey,
		KeyPassphrase:  form.KeyPassphrase,
		ConnectTimeout: connectTimeout,
	}
	h.collectWith(w, r, server, connReq, commandTimeout, servers.HasStoredCredentials(server))
}

// collectWith runs provider discovery, persists the snapshot batch, and
// renders the refreshed page.
func (h *Handlers) collectWith(w http.ResponseWriter, r *http.Request, server servers.Server, connReq sshclient.ConnectionRequest, commandTimeout time.Duration, hasStoredCreds bool) {
	collectCtx, cancelCollect := boundedCollectCtx(r.Context(), h.deps)
	defer cancelCollect()

	snapshots, probes, err := Collect(collectCtx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    commandTimeout,
	}, h.providers)
	if err != nil {
		h.renderPage(w, r, server, pageView{
			status:     http.StatusBadGateway,
			form:       h.defaultFormView(server, hasStoredCreds),
			deployForm: h.deployFormView(r, server, hasStoredCreds),
			collection: view.NodeCollectionResultView{
				Available: true,
				Error:     err.Error(),
			},
			flashKind:    "error",
			flashMessage: "Node discovery failed.",
		})
		return
	}

	collectedAt := latestCollectedAt(snapshots, probes)
	if err := h.repo.ReplaceLatest(r.Context(), server.ID, snapshots, collectedAt); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not persist node snapshots", "Node discovery ran but the latest snapshot could not be stored.")
		return
	}

	snapshotViews, err := h.storedSnapshotViews(r.Context(), server, hasStoredCreds)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not reload node snapshots", "Node discovery completed but the latest snapshot could not be reloaded.")
		return
	}

	flashMessage := fmt.Sprintf("Node discovery finished: %d node(s) found.", len(snapshots))
	if len(snapshots) == 0 {
		flashMessage = "Node discovery finished — no PasarGuard or Rebecca installation was found on this server."
	}

	h.renderPage(w, r, server, pageView{
		status:       http.StatusOK,
		form:         h.defaultFormView(server, hasStoredCreds),
		snapshots:    snapshotViews,
		collection:   collectionViewFromProbes(probes, collectedAt),
		deployForm:   h.deployFormView(r, server, hasStoredCreds),
		flashKind:    "success",
		flashMessage: flashMessage,
	})
}

// discoverAfterInstall runs a node discovery sweep and stores the result once an
// install has finished, so the new node shows up without a manual refresh. It
// runs in the install goroutine (request context is gone), best-effort: any
// failure is logged and swallowed.
func (h *Handlers) discoverAfterInstall(server servers.Server, connReq sshclient.ConnectionRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	snapshots, probes, err := Collect(ctx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    45 * time.Second,
	}, h.providers)
	if err != nil {
		slog.Warn("auto-discovery after install failed",
			slog.Int64("server_id", server.ID), slog.String("error", err.Error()))
		return
	}
	collectedAt := latestCollectedAt(snapshots, probes)
	if err := h.repo.ReplaceLatest(ctx, server.ID, snapshots, collectedAt); err != nil {
		slog.Warn("auto-discovery store after install failed",
			slog.Int64("server_id", server.ID), slog.String("error", err.Error()))
	}
}

// ── POST /servers/{id}/nodes/actions ──────────────────────────────────────────

func (h *Handlers) Action(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	if !servers.HasStoredCredentials(server) {
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-no-credentials", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid node action request", "The submitted node action could not be parsed.")
		return
	}

	nodeType := strings.TrimSpace(r.FormValue("node_type"))
	nodeName := strings.TrimSpace(r.FormValue("node_name"))
	actionKey := strings.TrimSpace(r.FormValue("action"))

	provider, ok := ProviderByType(h.providers, nodeType)
	if !ok {
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-invalid-action", http.StatusSeeOther)
		return
	}
	if err := ValidateNodeName(nodeName); err != nil {
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-invalid-action", http.StatusSeeOther)
		return
	}

	// Only act on nodes the latest discovery sweep actually found: the
	// (type, name) pair must come from stored evidence, not from a forged form.
	if !h.nodeDiscovered(r.Context(), server.ID, nodeType, nodeName) {
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-unknown-node", http.StatusSeeOther)
		return
	}

	command, timeout, err := provider.ActionCommand(nodeName, actionKey)
	if err != nil {
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-invalid-action", http.StatusSeeOther)
		return
	}

	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
	connReq := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
	}

	// Node actions run as background stream sessions (same pattern as the
	// command center): the POST redirects immediately and the page polls, so
	// long operations (update, uninstall) can never outlive the HTTP write
	// timeout.
	label := fmt.Sprintf("%s %s — %s", provider.DisplayName(), nodeName, actionKey)
	session := h.deps.CommandStreams.Create(server.ID, label)
	go h.runActionSession(session.ID, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		Command:           command,
		CommandTimeout:    timeout,
	})

	http.Redirect(w, r, nodesURL(server.ID)+"?stream="+url.QueryEscape(session.ID), http.StatusSeeOther)
}

func (h *Handlers) runActionSession(sessionID string, request sshclient.CommandRequest) {
	ctx := context.Background()
	result, runErr := h.deps.SSH.StreamCommand(ctx, request, sshclient.StreamHandlers{
		OnStdout: func(chunk string) { h.deps.CommandStreams.AppendStdout(sessionID, chunk) },
		OnStderr: func(chunk string) { h.deps.CommandStreams.AppendStderr(sessionID, chunk) },
	})
	if runErr != nil {
		h.deps.CommandStreams.Fail(sessionID, result.ExitCode, result.CompletedAt, runErr, 0)
		return
	}
	h.deps.CommandStreams.Complete(sessionID, result.ExitCode, result.CompletedAt, 0)
}

// nodeDiscovered reports whether the latest stored discovery sweep contains a
// node with the given type and name.
func (h *Handlers) nodeDiscovered(ctx context.Context, serverID int64, nodeType, nodeName string) bool {
	latest, err := h.repo.GetLatestByServer(ctx, serverID)
	if err != nil {
		return false
	}
	for _, snapshot := range latest {
		if snapshot.NodeType == nodeType && strings.EqualFold(snapshot.ServiceName, nodeName) {
			return true
		}
	}
	return false
}

// ── GET /servers/{id}/nodes/credentials ───────────────────────────────────────

// Credentials reads a PasarGuard node's API key and SSL certificate live over
// SSH (from /opt/<name>/.env and /var/lib/<name>/certs/ssl_cert.pem) and returns
// them as JSON for the copy-to-clipboard UI. The values are fetched on demand
// and never persisted — same security stance as the install flow. Read-only, so
// it's a GET (no CSRF) but still gated by auth + stored credentials.
func (h *Handlers) Credentials(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		writeNodeJSONError(w, http.StatusNotFound, "Server not found.")
		return
	}
	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		writeNodeJSONError(w, http.StatusNotFound, "Server not found.")
		return
	}
	if !servers.HasStoredCredentials(server) {
		writeNodeJSONError(w, http.StatusBadRequest, "Stored SSH credentials are required to read node credentials.")
		return
	}

	nodeType := strings.TrimSpace(r.URL.Query().Get("type"))
	nodeName := strings.TrimSpace(r.URL.Query().Get("name"))
	if nodeType != pasarguardType {
		writeNodeJSONError(w, http.StatusBadRequest, "Credentials are only available for PasarGuard nodes.")
		return
	}
	if err := ValidateNodeName(nodeName); err != nil {
		writeNodeJSONError(w, http.StatusBadRequest, "Invalid node name.")
		return
	}
	// Only read credentials for a node the latest discovery sweep actually found.
	if !h.nodeDiscovered(r.Context(), server.ID, nodeType, nodeName) {
		writeNodeJSONError(w, http.StatusNotFound, "That node is not part of the latest discovery sweep. Run discovery again first.")
		return
	}

	command, err := PasarGuardProvider{}.RegistrationInfoCommand(nodeName)
	if err != nil {
		writeNodeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
	ctx, cancel := context.WithTimeout(r.Context(), installInfoTimeout)
	defer cancel()
	result, runErr := h.deps.SSH.RunCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: sshclient.ConnectionRequest{
			Host:           server.Host,
			Port:           server.Port,
			Username:       server.Username,
			AuthMode:       server.AuthMode,
			Password:       password,
			PrivateKeyPEM:  privateKey,
			KeyPassphrase:  keyPassphrase,
			ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
		},
		Command:        command,
		CommandTimeout: installInfoTimeout,
	})
	if runErr != nil {
		writeNodeJSONError(w, http.StatusBadGateway, "Could not read node credentials over SSH: "+runErr.Error())
		return
	}

	info, hasAPIKey := ParseRegistrationInfo(nodeName, result.Stdout)
	writeNodeJSON(w, http.StatusOK, map[string]any{
		"node_name":   nodeName,
		"api_key":     info.APIKey,
		"has_api_key": hasAPIKey,
		"certificate": info.Certificate,
		"has_cert":    info.Certificate != "",
	})
}

func writeNodeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeNodeJSONError(w http.ResponseWriter, status int, msg string) {
	writeNodeJSON(w, status, map[string]string{"error": msg})
}

// ── POST /servers/{id}/nodes/install ──────────────────────────────────────────

// DeployStart handles the unified deploy form. One endpoint serves every engine:
// the posted node_type selects the provider, and the provider's own
// BuildInstallPlan validates and assembles the recipe. Adding an engine is
// therefore a provider + a form section, never a new route.
func (h *Handlers) DeployStart(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	if !servers.HasStoredCredentials(server) {
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-no-credentials", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid install request", "The submitted node install request could not be parsed.")
		return
	}

	nodeType := strings.TrimSpace(r.FormValue("node_type"))
	provider, ok := InstallableProviderByType(h.installableProviders, nodeType)
	if !ok {
		h.renderDeployForm(w, r, server, http.StatusUnprocessableEntity, InstallFormInput{},
			ValidationErrors{"node_type": "nodes.err_engine"})
		return
	}

	input := InstallFormInput{
		NodeType:    provider.Type(),
		NodeName:    strings.TrimSpace(r.FormValue("node_name")),
		ServicePort: strings.TrimSpace(r.FormValue("service_port")),
		APIPort:     strings.TrimSpace(r.FormValue("api_port")),
		Protocol:    strings.TrimSpace(r.FormValue("protocol")),
		APIKey:      strings.TrimSpace(r.FormValue("api_key")),
		SANEntries:  strings.TrimSpace(r.FormValue("san_entries")),
		Bundle:      r.FormValue("bundle"),
		Channel:     strings.TrimSpace(r.FormValue("channel")),
	}

	// A blank name means "the provider's default instance"; resolve it here so
	// the collision check and the job both use the real name.
	defaults := provider.InstallDefaults()
	nodeName := input.NodeName
	if nodeName == "" {
		nodeName = defaults.NodeName
	}

	installErrors := ValidationErrors{}
	if err := ValidateNodeName(nodeName); err != nil {
		installErrors["node_name"] = h.t(r, "nodes.err_node_name")
	} else if h.nodeNameTaken(r.Context(), server.ID, nodeName) {
		installErrors["node_name"] = h.t(r, "nodes.err_node_name_taken")
	}

	plan, cfgErr := provider.BuildInstallPlan(input)
	for field, key := range cfgErr {
		// Providers return i18n keys; translate for display.
		if _, taken := installErrors[field]; !taken {
			installErrors[field] = h.t(r, key)
		}
	}

	if installErrors.HasAny() {
		h.renderDeployForm(w, r, server, http.StatusUnprocessableEntity, input, installErrors)
		return
	}

	connReq := h.runtimeConnRequest(server)
	job := h.installs.create(server.ID, provider.Type(), nodeName, plan)
	go runInstall(job, h.deps.SSH, connReq, server.Host, func() { h.discoverAfterInstall(server, connReq) })

	http.Redirect(w, r, installJobURL(server.ID, job.id), http.StatusSeeOther)
}

// renderDeployForm re-renders the nodes page with the deploy modal open,
// preserving the user's entries and surfacing field errors.
func (h *Handlers) renderDeployForm(w http.ResponseWriter, r *http.Request, server servers.Server, status int, input InstallFormInput, formErrors ValidationErrors) {
	snapshots, err := h.storedSnapshotViews(r.Context(), server, true)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load node snapshots", "The latest node discovery snapshot could not be loaded.")
		return
	}
	h.renderPage(w, r, server, pageView{
		status:          status,
		form:            h.defaultFormView(server, true),
		snapshots:       snapshots,
		deployForm:      h.deployFormViewFromInput(r, server, true, input, formErrors),
		openDeployModal: true,
		flashKind:       "error",
		flashMessage:    "Please fix the deploy form before starting the installation.",
	})
}

// runtimeConnRequest builds the request-scoped SSH connection from a server's
// stored credentials.
func (h *Handlers) runtimeConnRequest(server servers.Server) sshclient.ConnectionRequest {
	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
	return sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
	}
}

// t translates a key using the request's active localizer, falling back to the
// default language. Used to localize provider-side validation messages.
func (h *Handlers) t(r *http.Request, key string, args ...any) string {
	loc := i18n.FromContext(r.Context())
	if loc == nil {
		loc = i18n.MustDefault().Localizer(i18n.DefaultLanguage)
	}
	return loc.T(key, args...)
}

// nodeNameTaken guards against re-running the install script over an existing
// instance, which would reinstall it.
func (h *Handlers) nodeNameTaken(ctx context.Context, serverID int64, nodeName string) bool {
	latest, err := h.repo.GetLatestByServer(ctx, serverID)
	if err != nil {
		return false
	}
	for _, snapshot := range latest {
		if snapshot.NodeType == "none" {
			continue
		}
		if strings.EqualFold(snapshot.ServiceName, nodeName) {
			return true
		}
	}
	return false
}

// ── GET /servers/{id}/nodes/install/{job} ─────────────────────────────────────

func (h *Handlers) InstallJob(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	job, found := h.installs.get(strings.TrimSpace(r.PathValue("job")))
	if !found || job.serverID != server.ID {
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-install-expired", http.StatusSeeOther)
		return
	}

	snap := job.snapshot()
	installView := view.NodeInstallView{
		Available:     true,
		JobID:         snap.ID,
		NodeType:      snap.NodeType,
		NodeName:      snap.NodeName,
		Status:        snap.Status,
		IsRunning:     snap.Status == installStatusRunning,
		StartedAt:     formatTimestamp(snap.CreatedAt),
		FinishedAt:    formatTimestamp(snap.FinishedAt),
		Output:        strings.TrimSpace(snap.Output),
		Error:         snap.Error,
		RefreshURL:    installJobURL(server.ID, snap.ID),
		RefreshMillis: streamRefreshMillis,
		NodesURL:      nodesURL(server.ID),
		ActiveStage:   snap.Stage,
		Stages:        h.installStageViews(r, snap),
	}
	durationEnd := snap.FinishedAt
	if durationEnd.IsZero() {
		durationEnd = time.Now().UTC()
	}
	installView.Duration = formatDuration(durationEnd.Sub(snap.CreatedAt))
	if snap.Info != nil {
		installView.Info = view.NodeRegistrationView{
			Available:   true,
			NodeName:    snap.Info.NodeName,
			NodeIP:      snap.Info.NodeIP,
			ServicePort: snap.Info.ServicePort,
			Protocol:    snap.Info.Protocol,
			APIPort:     snap.Info.APIPort,
			APIKey:      snap.Info.APIKey,
			Certificate: snap.Info.Certificate,
		}
	}

	page := view.NewPageData(h.deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("nodes.install_title")
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-node-install"
	page.PageTitle = page.T("nodes.install_page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("nodes.install_page_description")
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.NodeTarget = nodeTargetView(server)
	page.NodeInstall = installView
	page.PageStyles = []string{"/static/nodes.css"}

	if err := h.deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render node install page", http.StatusInternalServerError)
	}
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func (h *Handlers) loadServer(w http.ResponseWriter, r *http.Request) (servers.Server, bool) {
	serverID, ok := pathID(r)
	if !ok {
		httperrors.RenderPage(w, r, h.deps, servers.ErrNotFound, "/servers", "Server not found", "The requested server record does not exist.")
		return servers.Server{}, false
	}

	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load server", "The nodes page could not load the selected server.")
		return servers.Server{}, false
	}
	return server, true
}

func (h *Handlers) storedSnapshotViews(ctx context.Context, server servers.Server, actionsEnabled bool) ([]view.NodeSnapshotView, error) {
	snapshots := make([]view.NodeSnapshotView, 0)
	latest, err := h.repo.GetLatestByServer(ctx, server.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return snapshots, nil
		}
		return nil, err
	}
	// 30-day uptime per node, from the persisted per-sweep observations.
	// Best-effort: a read failure just renders the cards without the badge.
	uptime, err := h.repo.UptimeStats(ctx, server.ID, time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil {
		uptime = nil
	}
	for _, snapshot := range latest {
		v := h.snapshotView(snapshot, actionsEnabled)
		if stat, ok := uptime[UptimeKey(snapshot.NodeType, snapshot.ServiceName)]; ok && stat.Checks > 0 {
			v.UptimeKnown = true
			v.UptimePct = int(float64(stat.Running) / float64(stat.Checks) * 100)
			v.UptimeDetail = fmt.Sprintf("%d/%d", stat.Running, stat.Checks)
		}
		snapshots = append(snapshots, v)
	}
	return snapshots, nil
}

func (h *Handlers) snapshotView(snapshot Snapshot, actionsEnabled bool) view.NodeSnapshotView {
	snapshotView := view.NodeSnapshotView{
		Name:           fallbackDisplay(snapshot.ServiceName),
		NodeType:       fallbackDisplay(snapshot.NodeType),
		TypeLabel:      fallbackDisplay(snapshot.NodeType),
		InstallMode:    fallbackDisplay(snapshot.InstallMode),
		Version:        fallbackDisplay(snapshot.Version),
		HealthStatus:   fallbackDisplay(snapshot.HealthStatus),
		ActivePorts:    snapshot.ActivePorts,
		XrayPorts:      snapshot.XrayPorts,
		ServicePort:    fallbackDisplay(snapshot.ServicePort),
		APIPort:        fallbackDisplay(snapshot.APIPort),
		Protocol:       fallbackDisplay(snapshot.Protocol),
		DataDir:        fallbackDisplay(snapshot.DataDir),
		Confidence:     fallbackDisplay(snapshot.Confidence),
		Dependencies:   snapshot.Dependencies,
		Evidence:       snapshot.Evidence,
		CollectedAt:    formatTimestamp(snapshot.CollectedAt),
		ActionsEnabled: actionsEnabled,
	}
	if provider, ok := ProviderByType(h.providers, snapshot.NodeType); ok {
		snapshotView.TypeLabel = provider.DisplayName()
		for _, action := range provider.Actions() {
			snapshotView.Actions = append(snapshotView.Actions, view.NodeActionView{
				Key:    action.Key,
				Label:  action.Label,
				Icon:   action.Icon,
				Danger: action.Danger,
			})
		}
	}
	return snapshotView
}

func (h *Handlers) loadStreamView(streamID string, serverID int64) (view.CommandStreamView, string, string) {
	if streamID == "" || h.deps.CommandStreams == nil {
		return view.CommandStreamView{}, "", ""
	}

	snapshot, ok := h.deps.CommandStreams.Get(streamID)
	if !ok || snapshot.ServerID != serverID {
		return view.CommandStreamView{}, "error", "The requested node action session is no longer available."
	}

	flashKind := "success"
	flashMessage := "The node action is running — output refreshes automatically."
	switch snapshot.Status {
	case commandstream.StatusFailed:
		flashKind = "error"
		flashMessage = "The node action ended with an error."
	case commandstream.StatusCompleted:
		flashMessage = "Node action completed."
	}

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
		RefreshURL:    nodesURL(serverID) + "?stream=" + url.QueryEscape(snapshot.ID),
		RefreshMillis: streamRefreshMillis,
	}, flashKind, flashMessage
}

func (h *Handlers) renderPage(w http.ResponseWriter, r *http.Request, server servers.Server, state pageView) {
	page := view.NewPageData(h.deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("common.nodes")
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-nodes"
	page.PageTitle = page.T("nodes.page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("nodes.page_description")
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.NodeTarget = nodeTargetView(server)
	page.NodeForm = state.form
	page.NodeSnapshots = state.snapshots
	page.NodeFleet = buildNodeFleetView(state.snapshots)
	page.NodeCollection = state.collection
	page.NodeStream = state.stream
	deployForm := state.deployForm
	if deployForm.Action == "" {
		deployForm = h.deployFormView(r, server, servers.HasStoredCredentials(server))
	}
	page.NodeDeployForm = deployForm
	page.NodeDeployOpen = state.openDeployModal
	page.FlashKind = state.flashKind
	page.FlashMessage = state.flashMessage
	page.PageStyles = []string{"/static/nodes.css"}

	status := state.status
	if status == 0 {
		status = http.StatusOK
	}
	if err := h.deps.Renderer.Render(w, status, page); err != nil {
		http.Error(w, "render nodes page", http.StatusInternalServerError)
	}
}

func nodeTargetView(server servers.Server) view.NodeTargetView {
	return view.NodeTargetView{
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
}

// buildNodeFleetView aggregates the per-node snapshots into the command
// header's fleet counters. It runs on the server for every render so the
// header can never drift from the node cards below it (there is no
// client-side recomputation).
func buildNodeFleetView(snapshots []view.NodeSnapshotView) view.NodeFleetView {
	fleet := view.NodeFleetView{
		Total: len(snapshots),
	}
	portSet := make(map[string]struct{})
	var totalUptime, countUptime int

	// addPort ignores the "-" placeholder that fallbackDisplay substitutes for
	// an unknown value, so placeholders never inflate the port counter.
	addPort := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || p == "-" {
			return
		}
		portSet[p] = struct{}{}
	}

	for _, s := range snapshots {
		switch strings.ToLower(strings.TrimSpace(s.HealthStatus)) {
		case "running", "active", "healthy":
			fleet.Running++
		case "stopped", "inactive":
			fleet.Stopped++
		default:
			fleet.Unknown++
		}

		addPort(s.ServicePort)
		addPort(s.APIPort)
		for _, p := range s.ActivePorts {
			addPort(p)
		}

		if s.UptimeKnown {
			totalUptime += s.UptimePct
			countUptime++
		}
	}

	fleet.Ports = len(portSet)
	for port := range portSet {
		fleet.PortList = append(fleet.PortList, port)
	}
	sort.Strings(fleet.PortList)

	if countUptime > 0 {
		fleet.HealthPct = totalUptime / countUptime
		fleet.HealthKnown = true
	}

	return fleet
}

func (h *Handlers) defaultFormView(server servers.Server, hasStoredCreds bool) view.NodeFormView {
	return view.NodeFormView{
		Action:                     nodesURL(server.ID),
		ConnectTimeout:             h.deps.Config.SSH.ConnectTimeout.String(),
		CommandTimeout:             h.deps.Config.SSH.CommandTimeout.String(),
		StoredCredentialsAvailable: hasStoredCreds,
		RefreshURL:                 nodesURL(server.ID),
		Errors:                     map[string]string{},
	}
}

// deployEngines describes the selectable engines for the wizard's first step.
// It is derived from the registered installable providers so the UI can never
// advertise an engine the backend cannot install.
func (h *Handlers) deployEngines(r *http.Request) []view.NodeDeployEngineView {
	engines := make([]view.NodeDeployEngineView, 0, len(h.installableProviders))
	for i, provider := range h.installableProviders {
		meta := enginePresentation(provider.Type())
		defaults := provider.InstallDefaults()
		engines = append(engines, view.NodeDeployEngineView{
			Key:         provider.Type(),
			Label:       h.t(r, meta.labelKey),
			Description: h.t(r, meta.descriptionKey),
			Icon:        meta.icon,
			Kind:        meta.kind,
			Recommended: i == 0,
			Features:    []string{h.t(r, meta.featureOneKey), h.t(r, meta.featureTwoKey)},
			DefaultName: defaults.NodeName,
		})
	}
	return engines
}

// enginePresentation is the static, non-translatable presentation of an engine.
// Labels/descriptions are i18n keys so both locales stay in parity.
type enginePresentationMeta struct {
	labelKey       string
	descriptionKey string
	icon           string
	kind           string
	featureOneKey  string
	featureTwoKey  string
}

func enginePresentation(nodeType string) enginePresentationMeta {
	if nodeType == rebeccaType {
		return enginePresentationMeta{
			labelKey:       "nodes.engine_rebecca",
			descriptionKey: "nodes.engine_rebecca_desc",
			icon:           "package",
			kind:           "binary",
			featureOneKey:  "nodes.engine_rebecca_feature_1",
			featureTwoKey:  "nodes.engine_rebecca_feature_2",
		}
	}
	return enginePresentationMeta{
		labelKey:       "nodes.engine_pasarguard",
		descriptionKey: "nodes.engine_pasarguard_desc",
		icon:           "container",
		kind:           "docker",
		featureOneKey:  "nodes.engine_pasarguard_feature_1",
		featureTwoKey:  "nodes.engine_pasarguard_feature_2",
	}
}

// deployFormView returns the default deploy form (fresh page load), pre-filled
// from the recommended engine's defaults.
func (h *Handlers) deployFormView(r *http.Request, server servers.Server, enabled bool) view.NodeDeployFormView {
	provider := h.installableProviders[0]
	defaults := provider.InstallDefaults()
	return h.deployFormViewFromInput(r, server, enabled, InstallFormInput{
		NodeType:    provider.Type(),
		NodeName:    defaults.NodeName,
		ServicePort: defaults.ServicePort,
		APIPort:     defaults.APIPort,
		Protocol:    defaults.Protocol,
		Channel:     defaults.Channel,
	}, nil)
}

// deployFormViewFromInput re-renders the deploy modal preserving the user's
// entries. The certificate is never echoed back into the form (it is sensitive
// and should be re-pasted).
func (h *Handlers) deployFormViewFromInput(r *http.Request, server servers.Server, enabled bool, input InstallFormInput, formErrors ValidationErrors) view.NodeDeployFormView {
	if formErrors == nil {
		formErrors = ValidationErrors{}
	}

	nodeType := strings.TrimSpace(input.NodeType)
	if nodeType == "" {
		nodeType = h.installableProviders[0].Type()
	}
	provider, ok := InstallableProviderByType(h.installableProviders, nodeType)
	if !ok {
		provider = h.installableProviders[0]
		nodeType = provider.Type()
	}
	defaults := provider.InstallDefaults()

	nodeName := strings.TrimSpace(input.NodeName)
	if nodeName == "" {
		nodeName = defaults.NodeName
	}
	servicePort := strings.TrimSpace(input.ServicePort)
	if servicePort == "" {
		servicePort = defaults.ServicePort
	}
	apiPort := strings.TrimSpace(input.APIPort)
	if apiPort == "" {
		apiPort = defaults.APIPort
	}
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	if protocol == "" {
		protocol = defaults.Protocol
	}
	channel := strings.ToLower(strings.TrimSpace(input.Channel))
	if channel == "" {
		channel = defaults.Channel
	}

	channels := make([]view.NodeInstallChannelView, 0, 2)
	for _, c := range provider.InstallChannels() {
		channels = append(channels, view.NodeInstallChannelView{Key: c.Key, Label: c.Label, Enabled: c.Enabled})
	}

	return view.NodeDeployFormView{
		Action:      nodesURL(server.ID) + "/install",
		Engines:     h.deployEngines(r),
		NodeType:    nodeType,
		NodeName:    nodeName,
		ServicePort: servicePort,
		APIPort:     apiPort,
		Protocol:    protocol,
		APIKey:      strings.TrimSpace(input.APIKey),
		SANEntries:  strings.TrimSpace(input.SANEntries),
		Channel:     channel,
		Channels:    channels,
		Enabled:     enabled,
		Errors:      formErrors,
	}
}

func queryFlash(r *http.Request) (string, string) {
	switch r.URL.Query().Get("flash") {
	case "nodes-no-credentials":
		return "error", "Node actions need stored SSH credentials. Edit the server and store credentials first."
	case "nodes-invalid-action":
		return "error", "That node action is not supported."
	case "nodes-unknown-node":
		return "error", "That node is not part of the latest discovery sweep. Run discovery again first."
	case "nodes-install-expired":
		return "error", "That install session is no longer available (it expired or the panel restarted)."
	default:
		return "", ""
	}
}

func nodesURL(serverID int64) string {
	return "/servers/" + formatID(serverID) + "/nodes"
}

func installJobURL(serverID int64, jobID string) string {
	return nodesURL(serverID) + "/install/" + url.PathEscape(jobID)
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
		Action:         nodesURL(serverID),
		ConnectTimeout: input.ConnectTimeout,
		CommandTimeout: input.CommandTimeout,
		RefreshURL:     nodesURL(serverID),
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

func formatExitCode(value *int) string {
	if value == nil {
		return "n/a"
	}
	return strconv.Itoa(*value)
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

// installStageViews renders the ordered progress pipeline for the live install
// runner. Every stage is always present so the pills never shift position; the
// state marks which have completed, which is active, and which are still ahead.
// Per-stage durations come from the job's real stage timestamps.
func (h *Handlers) installStageViews(r *http.Request, snap installSnapshot) []view.NodeInstallStageView {
	views := make([]view.NodeInstallStageView, 0, len(InstallStageOrder))
	activeIndex := -1
	for i, stage := range InstallStageOrder {
		if stage == snap.Stage {
			activeIndex = i
			break
		}
	}
	settled := snap.Status != installStatusRunning

	for i, stage := range InstallStageOrder {
		state := "pending"
		switch {
		case activeIndex < 0:
			// No remote marker seen yet (or the job already finished): fall back
			// to the job status so a finished install never shows a live stage.
			if settled && stage == stageReady {
				state = "done"
			}
		case i < activeIndex:
			state = "done"
		case i == activeIndex:
			if settled {
				state = "done"
			} else {
				state = "active"
			}
		}
		if settled && stage == stageReady {
			state = "done"
		}

		detail := ""
		if startedAt, ok := snap.StageTimes[stage]; ok {
			end := snap.FinishedAt
			if nextStageAt, ok := nextStageTime(snap, stage); ok {
				end = nextStageAt
			}
			if end.IsZero() {
				end = time.Now().UTC()
			}
			detail = formatDuration(end.Sub(startedAt))
		}

		views = append(views, view.NodeInstallStageView{
			Key:    stage,
			Label:  h.t(r, stageLabelKey(stage)),
			State:  state,
			Detail: detail,
		})
	}
	return views
}

// nextStageTime returns when the pipeline moved past stage, so a stage's own
// duration can be measured. ok=false when there is no later stage yet.
func nextStageTime(snap installSnapshot, stage string) (time.Time, bool) {
	for i, candidate := range InstallStageOrder {
		if candidate != stage {
			continue
		}
		for _, later := range InstallStageOrder[i+1:] {
			if at, ok := snap.StageTimes[later]; ok {
				return at, true
			}
		}
		return time.Time{}, false
	}
	return time.Time{}, false
}
