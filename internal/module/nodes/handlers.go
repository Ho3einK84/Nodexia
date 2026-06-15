package nodes

import (
	"context"
	"encoding/json"
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

// Handlers serves the nodes route group.  All node knowledge (discovery,
// action commands, install support) comes from the configured providers.
type Handlers struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	repo       Repository
	providers  []Provider
	installs   *installStore
}

func NewHandlers(deps module.Dependencies, serverRepo servers.Repository, repo Repository, providers []Provider) *Handlers {
	if len(providers) == 0 {
		providers = DefaultProviders()
	}
	return &Handlers{
		deps:       deps,
		serverRepo: serverRepo,
		repo:       repo,
		providers:  providers,
		installs:   newInstallStore(),
	}
}

type FormInput struct {
	Password       string
	PrivateKey     string
	KeyPassphrase  string
	ConnectTimeout string
	CommandTimeout string
}

type ValidationErrors map[string]string

func (v ValidationErrors) HasAny() bool {
	return len(v) > 0
}

// pageView bundles everything the nodes page template renders.
type pageView struct {
	status       int
	form         view.NodeFormView
	snapshots    []view.NodeSnapshotView
	collection   view.NodeCollectionResultView
	stream       view.CommandStreamView
	installForm  view.NodeInstallFormView
	flashKind    string
	flashMessage string
}

// ── GET /servers/{id}/nodes ───────────────────────────────────────────────────

func (h *Handlers) Page(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	streamID := strings.TrimSpace(r.URL.Query().Get("stream"))
	wantRefresh := strings.TrimSpace(r.URL.Query().Get("refresh")) == "1"

	// Never kick off an SSH discovery sweep while the page is polling a live
	// action stream — only explicit refreshes or the first-ever visit collect.
	shouldCollect := hasStoredCreds && wantRefresh && streamID == ""
	if hasStoredCreds && !wantRefresh && streamID == "" {
		if hasStored, _ := h.repo.HasAny(r.Context(), server.ID); !hasStored {
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
		status:      http.StatusOK,
		form:        h.defaultFormView(server, hasStoredCreds),
		snapshots:   snapshots,
		installForm: h.installFormView(server, hasStoredCreds),
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
			installForm:  h.installFormView(server, false),
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
			status:      http.StatusBadGateway,
			form:        h.defaultFormView(server, hasStoredCreds),
			installForm: h.installFormView(server, hasStoredCreds),
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
		installForm:  h.installFormView(server, hasStoredCreds),
		flashKind:    "success",
		flashMessage: flashMessage,
	})
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

func (h *Handlers) InstallStart(w http.ResponseWriter, r *http.Request) {
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

	nodeName := strings.TrimSpace(r.FormValue("node_name"))
	input := installFormInput{
		NodeName:    nodeName,
		ServicePort: strings.TrimSpace(r.FormValue("service_port")),
		APIPort:     strings.TrimSpace(r.FormValue("api_port")),
		Protocol:    strings.TrimSpace(r.FormValue("protocol")),
		APIKey:      strings.TrimSpace(r.FormValue("api_key")),
	}

	installErrors := ValidationErrors{}
	if err := ValidateNodeName(nodeName); err != nil {
		installErrors["node_name"] = "Use letters, digits, dot, dash, or underscore (max 64 characters)."
	} else if h.nodeNameTaken(r.Context(), server.ID, nodeName) {
		installErrors["node_name"] = "A node with this name already exists on this server — pick another name."
	}

	config, cfgErr := PasarGuardProvider{}.normalizeInstallInput(input)
	if cfgErr != nil {
		for field, message := range cfgErr {
			installErrors[field] = message
		}
	}

	if installErrors.HasAny() {
		snapshots, err := h.storedSnapshotViews(r.Context(), server, true)
		if err != nil {
			httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load node snapshots", "The latest node discovery snapshot could not be loaded.")
			return
		}
		h.renderPage(w, r, server, pageView{
			status:       http.StatusUnprocessableEntity,
			form:         h.defaultFormView(server, true),
			snapshots:    snapshots,
			installForm:  h.installFormViewFromInput(server, true, input, installErrors),
			flashKind:    "error",
			flashMessage: "Please fix the install form before starting the installation.",
		})
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

	job := h.installs.create(server.ID, nodeName, config)
	go runInstall(job, h.deps.SSH, connReq, PasarGuardProvider{}, server.Host)

	http.Redirect(w, r, installJobURL(server.ID, job.id), http.StatusSeeOther)
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
			APIKey:      snap.Info.APIKey,
			Certificate: snap.Info.Certificate,
		}
	}

	page := view.NewPageData(h.deps.Config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Install node"
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-node-install"
	page.PageTitle = "Installing PasarGuard node on " + server.Name
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = "The official PasarGuard install script is running over SSH. After it finishes, the API key and SSL certificate needed to register the node in the panel are shown here."
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
	for _, snapshot := range latest {
		snapshots = append(snapshots, h.snapshotView(snapshot, actionsEnabled))
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
	if snapshot.Status == commandstream.StatusFailed {
		flashKind = "error"
		flashMessage = "The node action ended with an error."
	} else if snapshot.Status == commandstream.StatusCompleted {
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
	page := view.NewPageData(h.deps.Config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Nodes"
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-nodes"
	page.PageTitle = "Nodes on " + server.Name
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = "Discover PasarGuard and Rebecca node installations over SSH, manage them through their official CLIs, and install new PasarGuard nodes."
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.NodeTarget = nodeTargetView(server)
	page.NodeForm = state.form
	page.NodeSnapshots = state.snapshots
	page.NodeCollection = state.collection
	page.NodeStream = state.stream
	page.NodeInstallForm = state.installForm
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

// installFormInput is the raw pre-install configuration submitted by the user.
type installFormInput struct {
	NodeName    string
	ServicePort string
	APIPort     string
	Protocol    string
	APIKey      string
}

// installFormView returns the default install form (fresh page load).
func (h *Handlers) installFormView(server servers.Server, enabled bool) view.NodeInstallFormView {
	return h.installFormViewFromInput(server, enabled, installFormInput{
		ServicePort: pasarguardDefaultPort,
		Protocol:    pasarguardDefaultProtocol,
	}, nil)
}

// installFormViewFromInput re-renders the install form preserving the user's
// entries and surfacing field errors.
func (h *Handlers) installFormViewFromInput(server servers.Server, enabled bool, input installFormInput, formErrors ValidationErrors) view.NodeInstallFormView {
	if formErrors == nil {
		formErrors = ValidationErrors{}
	}
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	if protocol == "" {
		protocol = pasarguardDefaultProtocol
	}
	return view.NodeInstallFormView{
		Action:      nodesURL(server.ID) + "/install",
		NodeName:    input.NodeName,
		ServicePort: input.ServicePort,
		APIPort:     input.APIPort,
		Protocol:    protocol,
		APIKey:      input.APIKey,
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
