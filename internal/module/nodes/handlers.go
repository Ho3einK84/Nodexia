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
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

const streamRefreshMillis = 2000

// Handlers serves the nodes route group. All node knowledge (discovery,
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
	hasSnapshot     bool
	collection      view.NodeCollectionResultView
	stream          view.CommandStreamView
	deployForm      view.NodeDeployFormView
	openDeployModal bool
	flashKind       string
	flashMessage    string
}

// ── GET /servers/{id}/nodes (strictly read-only) ──────────────────────────────

func (h *Handlers) Page(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	streamID := strings.TrimSpace(r.URL.Query().Get("stream"))

	snapshots, hasSnapshot, lastSweep, err := h.storedSnapshotViews(r.Context(), r, server, hasStoredCreds)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers",
			h.t(r, "nodes.err_snapshot_load_title"),
			h.t(r, "nodes.err_snapshot_load_msg"))
		return
	}

	collection := view.NodeCollectionResultView{
		HasSnapshot: hasSnapshot,
	}
	if !lastSweep.IsZero() {
		collection.CollectedAt = formatTimestamp(lastSweep)
		collection.LastSweepISO = lastSweep.UTC().Format(time.RFC3339)
	}

	page := pageView{
		status:      http.StatusOK,
		form:        h.defaultFormView(server, hasStoredCreds),
		snapshots:   snapshots,
		hasSnapshot: hasSnapshot,
		collection:  collection,
		deployForm:  h.deployFormView(r, server, hasStoredCreds),
	}
	page.stream, page.flashKind, page.flashMessage = h.loadStreamView(r, streamID, server.ID)
	if page.flashKind == "" {
		page.flashKind, page.flashMessage = h.queryFlash(r)
	}

	h.renderPage(w, r, server, page)
}

// ── POST /servers/{id}/nodes (CSRF-protected discovery refresh) ───────────────

func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	if hasStoredCreds {
		connReq := h.runtimeConnRequest(server)
		h.collectWith(w, r, server, connReq, h.deps.Config.SSH.CommandTimeout, true)
		return
	}

	if err := r.ParseForm(); err != nil {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.err_refresh_parse_msg"))
			return
		}
		httperrors.RenderPage(w, r, h.deps, err, "/servers",
			h.t(r, "nodes.err_refresh_parse_title"),
			h.t(r, "nodes.err_refresh_parse_msg"))
		return
	}

	form := formInputFromRequest(r, h.deps)
	validationErrors, connectTimeout, commandTimeout := h.validateForm(r, form, server, h.deps)
	if validationErrors.HasAny() {
		if wantsJSON(r) {
			writeNodeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"ok":     false,
				"error":  h.t(r, "nodes.flash_form_fix"),
				"errors": validationErrors,
			})
			return
		}
		cached, hasSnap, _, _ := h.storedSnapshotViews(r.Context(), r, server, false)
		h.renderPage(w, r, server, pageView{
			status:       http.StatusUnprocessableEntity,
			form:         formViewFromInput(form, validationErrors, server.ID),
			snapshots:    cached,
			hasSnapshot:  hasSnap,
			deployForm:   h.deployFormView(r, server, false),
			flashKind:    "error",
			flashMessage: h.t(r, "nodes.flash_form_fix"),
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
	h.collectWith(w, r, server, connReq, commandTimeout, false)
}

// collectWith serializes per-server sweeps via AcquireServerSweep, executes
// provider discovery, preserves cached nodes on transient partial/total SSH
// failures, persists clean results, and returns JSON or HTML.
func (h *Handlers) collectWith(w http.ResponseWriter, r *http.Request, server servers.Server, connReq sshclient.ConnectionRequest, commandTimeout time.Duration, hasStoredCreds bool) {
	collectCtx, cancelCollect := boundedCollectCtx(r.Context(), h.deps)
	defer cancelCollect()

	release, lockErr := AcquireServerSweep(collectCtx, server.ID)
	if lockErr != nil {
		h.renderCollectFailure(w, r, server, nil, hasStoredCreds, lockErr)
		return
	}
	defer release()

	snapshots, probes, collectErr := Collect(collectCtx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    commandTimeout,
	}, h.providers)

	persisted, partial, persistErr := PersistDiscoverySweep(
		r.Context(),
		h.repo,
		server.ID,
		snapshots,
		probes,
		collectErr,
		SweepSourceManual,
		func(fn func() error) error { return db.RetryOnBusy(r.Context(), fn) },
	)

	if persistErr != nil && !partial {
		h.renderCollectFailure(w, r, server, probes, hasStoredCreds, persistErr)
		return
	}

	snapshotViews, hasSnapshot, lastSweep, err := h.storedSnapshotViews(r.Context(), r, server, hasStoredCreds)
	if err != nil {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusInternalServerError, h.t(r, "nodes.err_snapshot_reload_msg"))
			return
		}
		httperrors.RenderPage(w, r, h.deps, err, "/servers",
			h.t(r, "nodes.err_snapshot_reload_title"),
			h.t(r, "nodes.err_snapshot_reload_msg"))
		return
	}

	collectedAt := latestDiscoveryTime(persisted, probes)
	collection := collectionViewFromProbes(probes, collectedAt)
	collection.HasSnapshot = hasSnapshot
	collection.Partial = partial
	if !lastSweep.IsZero() {
		collection.LastSweepISO = lastSweep.UTC().Format(time.RFC3339)
	}

	fleet := buildNodeFleetView(snapshotViews)
	fleet.InstallingCount = h.installs.countRunning(server.ID)
	fleet.EmptyState = emptyStateFor(snapshotViews, hasSnapshot)

	flashKind := "success"
	var flashMessage string
	switch {
	case partial && persistErr != nil:
		flashKind = "warning"
		flashMessage = h.t(r, "nodes.flash_refresh_partial")
		collection.Error = persistErr.Error()
	case len(snapshotViews) == 0:
		flashMessage = h.t(r, "nodes.flash_refresh_none")
	default:
		flashMessage = h.t(r, "nodes.flash_refresh_found", "count", len(snapshotViews))
	}

	if wantsJSON(r) {
		writeNodeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"partial":       partial,
			"flash_kind":    flashKind,
			"flash_message": flashMessage,
			"snapshots":     snapshotViews,
			"fleet":         fleet,
			"collection":    collection,
		})
		return
	}

	h.renderPage(w, r, server, pageView{
		status:       http.StatusOK,
		form:         h.defaultFormView(server, hasStoredCreds),
		snapshots:    snapshotViews,
		hasSnapshot:  hasSnapshot,
		collection:   collection,
		deployForm:   h.deployFormView(r, server, hasStoredCreds),
		flashKind:    flashKind,
		flashMessage: flashMessage,
	})
}

func (h *Handlers) renderCollectFailure(w http.ResponseWriter, r *http.Request, server servers.Server, probes []ProbeReport, hasStoredCreds bool, cause error) {
	cached, hasSnapshot, lastSweep, _ := h.storedSnapshotViews(r.Context(), r, server, hasStoredCreds)
	collection := collectionViewFromProbes(probes, lastSweep)
	collection.Available = true
	collection.HasSnapshot = hasSnapshot
	collection.Error = cause.Error()
	if !lastSweep.IsZero() {
		collection.LastSweepISO = lastSweep.UTC().Format(time.RFC3339)
	}
	fleet := buildNodeFleetView(cached)
	fleet.InstallingCount = h.installs.countRunning(server.ID)
	fleet.EmptyState = emptyStateFor(cached, hasSnapshot)
	msg := h.t(r, "nodes.flash_refresh_failed")

	if wantsJSON(r) {
		writeNodeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":            false,
			"error":         cause.Error(),
			"flash_kind":    "error",
			"flash_message": msg,
			"snapshots":     cached,
			"fleet":         fleet,
			"collection":    collection,
		})
		return
	}

	h.renderPage(w, r, server, pageView{
		status:       http.StatusBadGateway,
		form:         h.defaultFormView(server, hasStoredCreds),
		snapshots:    cached,
		hasSnapshot:  hasSnapshot,
		deployForm:   h.deployFormView(r, server, hasStoredCreds),
		collection:   collection,
		flashKind:    "error",
		flashMessage: msg,
	})
}

func (h *Handlers) discoverAfterInstall(server servers.Server, connReq sshclient.ConnectionRequest) {
	h.discoverBackgroundWithSource(server, connReq, SweepSourceAction)
}

func (h *Handlers) discoverBackgroundWithSource(server servers.Server, connReq sshclient.ConnectionRequest, source string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	release, err := AcquireServerSweep(ctx, server.ID)
	if err != nil {
		return
	}
	defer release()

	snapshots, probes, collectErr := Collect(ctx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    45 * time.Second,
	}, h.providers)
	_, _, persistErr := PersistDiscoverySweep(
		ctx,
		h.repo,
		server.ID,
		snapshots,
		probes,
		collectErr,
		source,
		func(fn func() error) error { return db.RetryOnBusy(ctx, fn) },
	)
	if persistErr != nil {
		slog.Warn("background node discovery failed",
			slog.Int64("server_id", server.ID),
			slog.String("source", source),
			slog.String("error", persistErr.Error()))
	}
}

// ── POST /servers/{id}/nodes/actions ──────────────────────────────────────────

func (h *Handlers) Action(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	if !servers.HasStoredCredentials(server) {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.flash_no_credentials"))
			return
		}
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-no-credentials", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.err_action_parse_msg"))
			return
		}
		httperrors.RenderPage(w, r, h.deps, err, "/servers",
			h.t(r, "nodes.err_action_parse_title"),
			h.t(r, "nodes.err_action_parse_msg"))
		return
	}

	nodeType := strings.TrimSpace(r.FormValue("node_type"))
	nodeName := strings.TrimSpace(r.FormValue("node_name"))
	actionKey := strings.TrimSpace(r.FormValue("action"))

	provider, ok := ProviderByType(h.providers, nodeType)
	if !ok {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.flash_invalid_action"))
			return
		}
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-invalid-action", http.StatusSeeOther)
		return
	}
	if err := ValidateNodeName(nodeName); err != nil {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.json_err_invalid_name"))
			return
		}
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-invalid-action", http.StatusSeeOther)
		return
	}

	if !h.nodeDiscovered(r.Context(), server.ID, nodeType, nodeName) {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.flash_unknown_node"))
			return
		}
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-unknown-node", http.StatusSeeOther)
		return
	}

	command, timeout, err := provider.ActionCommand(nodeName, actionKey)
	if err != nil {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.flash_invalid_action"))
			return
		}
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-invalid-action", http.StatusSeeOther)
		return
	}

	connReq := h.runtimeConnRequest(server)
	actionLabel := actionKey
	if act, found := actionByKey(provider.Actions(), actionKey); found {
		actionLabel = h.translateActionLabel(r, act.Label)
	}
	label := fmt.Sprintf("%s · %s — %s", provider.DisplayName(), nodeName, actionLabel)
	session := h.deps.CommandStreams.Create(server.ID, label)
	shouldRefreshAfter := isMutatingNodeAction(actionKey)

	go h.runActionSession(session.ID, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		Command:           command,
		CommandTimeout:    timeout,
	}, func() {
		if shouldRefreshAfter {
			h.discoverBackgroundWithSource(server, connReq, SweepSourceAction)
		}
	})

	if wantsJSON(r) {
		writeNodeJSON(w, http.StatusAccepted, map[string]any{
			"ok":         true,
			"stream_id":  session.ID,
			"label":      label,
			"events_url": nodesURL(server.ID) + "/stream/" + url.PathEscape(session.ID) + "/events",
		})
		return
	}

	http.Redirect(w, r, nodesURL(server.ID)+"?stream="+url.QueryEscape(session.ID), http.StatusSeeOther)
}

func isMutatingNodeAction(actionKey string) bool {
	switch strings.TrimSpace(actionKey) {
	case "start", "stop", "restart", "update", "core-update", "renew-cert", "reinstall", "uninstall":
		return true
	default:
		return false
	}
}

func (h *Handlers) runActionSession(sessionID string, request sshclient.CommandRequest, afterSuccess func()) {
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
	if afterSuccess != nil && (result.ExitCode == nil || *result.ExitCode == 0) {
		afterSuccess()
	}
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

func (h *Handlers) Credentials(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_server_not_found"))
		return
	}
	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		if errors.Is(err, servers.ErrNotFound) {
			writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_server_not_found"))
			return
		}
		writeNodeJSONError(w, http.StatusInternalServerError, h.t(r, "nodes.json_err_server_internal"))
		return
	}
	if !servers.HasStoredCredentials(server) {
		writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.json_err_creds_required"))
		return
	}

	nodeType := strings.TrimSpace(r.URL.Query().Get("type"))
	nodeName := strings.TrimSpace(r.URL.Query().Get("name"))
	if nodeType != pasarguardType {
		writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.json_err_pasarguard_only"))
		return
	}
	if err := ValidateNodeName(nodeName); err != nil {
		writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.json_err_invalid_name"))
		return
	}
	if !h.nodeDiscovered(r.Context(), server.ID, nodeType, nodeName) {
		writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_node_not_discovered"))
		return
	}

	command, err := PasarGuardProvider{}.RegistrationInfoCommand(nodeName)
	if err != nil {
		writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.json_err_invalid_name"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), installInfoTimeout)
	defer cancel()
	result, runErr := h.deps.SSH.RunCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: h.runtimeConnRequest(server),
		Command:           command,
		CommandTimeout:    installInfoTimeout,
	})
	if runErr != nil {
		writeNodeJSONError(w, http.StatusBadGateway, h.t(r, "nodes.json_err_ssh_failed", "error", runErr.Error()))
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

// ── GET /servers/{id}/nodes/config ────────────────────────────────────────────

// ViewConfig reads /opt/<name>/.env cleanly over SSH for both PasarGuard and
// Rebecca nodes and masks sensitive secret fields before returning JSON.
func (h *Handlers) ViewConfig(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_server_not_found"))
		return
	}
	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		if errors.Is(err, servers.ErrNotFound) {
			writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_server_not_found"))
			return
		}
		writeNodeJSONError(w, http.StatusInternalServerError, h.t(r, "nodes.json_err_server_internal"))
		return
	}
	if !servers.HasStoredCredentials(server) {
		writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.json_err_creds_required"))
		return
	}

	nodeType := strings.TrimSpace(r.URL.Query().Get("type"))
	nodeName := strings.TrimSpace(r.URL.Query().Get("name"))
	if _, ok := ProviderByType(h.providers, nodeType); !ok {
		writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.flash_invalid_action"))
		return
	}
	if err := ValidateNodeName(nodeName); err != nil {
		writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.json_err_invalid_name"))
		return
	}
	if !h.nodeDiscovered(r.Context(), server.ID, nodeType, nodeName) {
		writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_node_not_discovered"))
		return
	}

	command := `sh -c '` + sudoPreamble +
		`$SUDO cat /opt/` + nodeName + `/.env 2>/dev/null || true'`
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, runErr := h.deps.SSH.RunCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: h.runtimeConnRequest(server),
		Command:           command,
		CommandTimeout:    30 * time.Second,
	})
	if runErr != nil {
		writeNodeJSONError(w, http.StatusBadGateway, h.t(r, "nodes.json_err_ssh_failed", "error", runErr.Error()))
		return
	}

	env := parseEnvFile(strings.Split(result.Stdout, "\n"))
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type configEntry struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Redacted bool   `json:"redacted"`
	}
	entries := make([]configEntry, 0, len(keys))
	redactedVal := h.t(r, "nodes.config_secret_redacted")
	for _, k := range keys {
		redacted := isSecretEnvKey(k)
		val := env[k]
		if redacted {
			val = redactedVal
		}
		entries = append(entries, configEntry{
			Key:      k,
			Value:    val,
			Redacted: redacted,
		})
	}

	writeNodeJSON(w, http.StatusOK, map[string]any{
		"node_type":   nodeType,
		"node_name":   nodeName,
		"config_path": "/opt/" + nodeName + "/.env",
		"entries":     entries,
	})
}

func isSecretEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, tok := range []string{"KEY", "SECRET", "PASS", "TOKEN", "PRIVATE"} {
		if strings.Contains(upper, tok) {
			return true
		}
	}
	return false
}

func writeNodeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeNodeJSONError(w http.ResponseWriter, status int, msg string) {
	writeNodeJSON(w, status, map[string]string{"error": msg})
}

func wantsJSON(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "application/json") || strings.EqualFold(r.Header.Get("X-Requested-With"), "fetch")
}

// ── POST /servers/{id}/nodes/install ──────────────────────────────────────────

func (h *Handlers) DeployStart(w http.ResponseWriter, r *http.Request) {
	server, ok := h.loadServer(w, r)
	if !ok {
		return
	}

	if !servers.HasStoredCredentials(server) {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.flash_no_credentials"))
			return
		}
		http.Redirect(w, r, nodesURL(server.ID)+"?flash=nodes-no-credentials", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusBadRequest, h.t(r, "nodes.err_install_parse_msg"))
			return
		}
		httperrors.RenderPage(w, r, h.deps, err, "/servers",
			h.t(r, "nodes.err_install_parse_title"),
			h.t(r, "nodes.err_install_parse_msg"))
		return
	}

	nodeType := strings.TrimSpace(r.FormValue("node_type"))
	provider, ok := InstallableProviderByType(h.installableProviders, nodeType)
	if !ok {
		errs := ValidationErrors{"node_type": h.t(r, "nodes.err_engine")}
		if wantsJSON(r) {
			writeNodeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"ok":     false,
				"error":  h.t(r, "nodes.flash_deploy_fix"),
				"errors": errs,
			})
			return
		}
		h.renderDeployForm(w, r, server, http.StatusUnprocessableEntity, InstallFormInput{}, errs)
		return
	}

	rawOverride := strings.ToLower(strings.TrimSpace(r.FormValue("confirm_override")))
	confirmOverride := rawOverride == "1" || rawOverride == "true" || rawOverride == "on"

	input := InstallFormInput{
		NodeType:        provider.Type(),
		NodeName:        strings.TrimSpace(r.FormValue("node_name")),
		ServicePort:     strings.TrimSpace(r.FormValue("service_port")),
		APIPort:         strings.TrimSpace(r.FormValue("api_port")),
		Protocol:        strings.TrimSpace(r.FormValue("protocol")),
		APIKey:          strings.TrimSpace(r.FormValue("api_key")),
		SANEntries:      strings.TrimSpace(r.FormValue("san_entries")),
		Bundle:          r.FormValue("bundle"),
		Channel:         strings.TrimSpace(r.FormValue("channel")),
		ConfirmOverride: confirmOverride,
	}

	defaults := provider.InstallDefaults()
	nodeName := input.NodeName
	if nodeName == "" {
		nodeName = defaults.NodeName
	}

	installErrors := ValidationErrors{}
	if err := ValidateNodeName(nodeName); err != nil {
		installErrors["node_name"] = h.t(r, "nodes.err_node_name")
	} else if !confirmOverride && h.nodeNameTaken(r.Context(), server.ID, nodeName) {
		installErrors["node_name"] = h.t(r, "nodes.err_node_name_taken")
	}

	plan, cfgErr := provider.BuildInstallPlan(input)
	for field, key := range cfgErr {
		if _, taken := installErrors[field]; !taken {
			installErrors[field] = h.t(r, key)
		}
	}

	if installErrors.HasAny() {
		if wantsJSON(r) {
			writeNodeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"ok":     false,
				"error":  h.t(r, "nodes.flash_deploy_fix"),
				"errors": installErrors,
			})
			return
		}
		h.renderDeployForm(w, r, server, http.StatusUnprocessableEntity, input, installErrors)
		return
	}

	connReq := h.runtimeConnRequest(server)
	job := h.installs.create(server.ID, provider.Type(), nodeName, plan)
	go runInstall(job, h.deps.SSH, connReq, server.Host, func() { h.discoverAfterInstall(server, connReq) })

	if wantsJSON(r) {
		snap := job.snapshot()
		writeNodeJSON(w, http.StatusAccepted, map[string]any{
			"ok":         true,
			"job_id":     job.id,
			"node_type":  provider.Type(),
			"node_name":  nodeName,
			"status_url": installJobURL(server.ID, job.id),
			"events_url": installJobURL(server.ID, job.id) + "/events",
			"cancel_url": installJobURL(server.ID, job.id) + "/cancel",
			"stages":     h.installStageViews(r, snap),
		})
		return
	}

	http.Redirect(w, r, installJobURL(server.ID, job.id), http.StatusSeeOther)
}

func (h *Handlers) InstallCancel(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_server_not_found"))
		return
	}
	job, found := h.installs.get(strings.TrimSpace(r.PathValue("job")))
	if !found || job.serverID != serverID {
		writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_install_not_found"))
		return
	}
	if !job.Cancel() {
		writeNodeJSONError(w, http.StatusConflict, h.t(r, "nodes.json_err_install_finished"))
		return
	}
	writeNodeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"status": installStatusCanceled,
	})
}

func (h *Handlers) renderDeployForm(w http.ResponseWriter, r *http.Request, server servers.Server, status int, input InstallFormInput, formErrors ValidationErrors) {
	snapshots, hasSnapshot, lastSweep, err := h.storedSnapshotViews(r.Context(), r, server, true)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers",
			h.t(r, "nodes.err_snapshot_load_title"),
			h.t(r, "nodes.err_snapshot_load_msg"))
		return
	}
	collection := view.NodeCollectionResultView{HasSnapshot: hasSnapshot}
	if !lastSweep.IsZero() {
		collection.CollectedAt = formatTimestamp(lastSweep)
		collection.LastSweepISO = lastSweep.UTC().Format(time.RFC3339)
	}
	h.renderPage(w, r, server, pageView{
		status:          status,
		form:            h.defaultFormView(server, true),
		snapshots:       snapshots,
		hasSnapshot:     hasSnapshot,
		collection:      collection,
		deployForm:      h.deployFormViewFromInput(r, server, true, input, formErrors),
		openDeployModal: true,
		flashKind:       "error",
		flashMessage:    h.t(r, "nodes.flash_deploy_fix"),
	})
}

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

func (h *Handlers) t(r *http.Request, key string, args ...any) string {
	loc := i18n.FromContext(r.Context())
	if loc == nil {
		loc = i18n.MustDefault().Localizer(i18n.DefaultLanguage)
	}
	return loc.T(key, args...)
}

func (h *Handlers) translateActionLabel(r *http.Request, label string) string {
	if strings.HasPrefix(label, "nodes.") {
		return h.t(r, label)
	}
	return label
}

func (h *Handlers) nodeNameTaken(ctx context.Context, serverID int64, nodeName string) bool {
	latest, err := h.repo.GetLatestByServer(ctx, serverID)
	if err != nil {
		return false
	}
	for _, snapshot := range latest {
		if isPlaceholderNodeType(snapshot.NodeType) {
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
		if wantsJSON(r) {
			writeNodeJSONError(w, http.StatusNotFound, h.t(r, "nodes.json_err_install_not_found"))
			return
		}
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

	if wantsJSON(r) {
		writeNodeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"install": installView,
		})
		return
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
		httperrors.RenderPage(w, r, h.deps, servers.ErrNotFound, "/servers",
			h.t(r, "nodes.err_server_missing_title"),
			h.t(r, "nodes.err_server_missing_msg"))
		return servers.Server{}, false
	}

	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers",
			h.t(r, "nodes.err_server_load_title"),
			h.t(r, "nodes.err_server_load_msg"))
		return servers.Server{}, false
	}
	return server, true
}

func (h *Handlers) storedSnapshotViews(ctx context.Context, r *http.Request, server servers.Server, actionsEnabled bool) ([]view.NodeSnapshotView, bool, time.Time, error) {
	snapshots := make([]view.NodeSnapshotView, 0)
	latest, err := h.repo.GetLatestByServer(ctx, server.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return snapshots, false, time.Time{}, nil
		}
		return nil, false, time.Time{}, err
	}

	var lastSweep time.Time
	for _, s := range latest {
		if s.CollectedAt.After(lastSweep) {
			lastSweep = s.CollectedAt
		}
	}

	uptime, err := h.repo.UptimeStats(ctx, server.ID, time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil {
		uptime = nil
	}

	for _, snapshot := range latest {
		if isPlaceholderNodeType(snapshot.NodeType) {
			continue
		}
		v := h.snapshotView(r, snapshot, actionsEnabled)
		if stat, ok := uptime[UptimeKey(snapshot.NodeType, snapshot.ServiceName)]; ok {
			if stat.Checks > 0 {
				v.UptimeKnown = true
				v.UptimePct = stat.Percent
				v.UptimeDetail = fmt.Sprintf("%d/%d", stat.Running, stat.Checks)
			}
			v.UptimeDays = make([]view.NodeUptimeDayView, 0, len(stat.Days))
			for _, day := range stat.Days {
				tip := h.t(r, "nodes.uptime_day_empty", "date", day.Date)
				if day.Checks > 0 {
					tip = h.t(r, "nodes.uptime_day_tooltip",
						"date", day.Date,
						"pct", day.Percent,
						"running", day.Running,
						"checks", day.Checks)
				}
				v.UptimeDays = append(v.UptimeDays, view.NodeUptimeDayView{
					Date:    day.Date,
					Status:  day.Status,
					Checks:  day.Checks,
					Running: day.Running,
					Percent: day.Percent,
					Tooltip: tip,
				})
			}
		} else {
			v.UptimeDays = emptyUptimeDays(time.Now().UTC(), func(date string) string {
				return h.t(r, "nodes.uptime_day_empty", "date", date)
			})
		}
		snapshots = append(snapshots, v)
	}
	return snapshots, true, lastSweep, nil
}

func emptyUptimeDays(now time.Time, tooltipFn func(string) string) []view.NodeUptimeDayView {
	todayUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startUTC := todayUTC.AddDate(0, 0, -(uptimeStripDays - 1))
	out := make([]view.NodeUptimeDayView, 0, uptimeStripDays)
	for offset := 0; offset < uptimeStripDays; offset++ {
		dateStr := startUTC.AddDate(0, 0, offset).Format("2006-01-02")
		out = append(out, view.NodeUptimeDayView{
			Date:    dateStr,
			Status:  "empty",
			Percent: -1,
			Tooltip: tooltipFn(dateStr),
		})
	}
	return out
}

func (h *Handlers) snapshotView(r *http.Request, snapshot Snapshot, actionsEnabled bool) view.NodeSnapshotView {
	healthLower := strings.ToLower(strings.TrimSpace(snapshot.HealthStatus))
	isRunning := IsRunningHealth(healthLower)
	isRestarting := healthLower == "restarting" || healthLower == "activating"
	isDegraded := healthLower == "degraded" || healthLower == "unhealthy"
	isStopped := healthLower == "stopped" || healthLower == "inactive"

	lastSeenStr := h.t(r, "nodes.never_seen")
	lastSeenISO := ""
	if !snapshot.LastSeenAt.IsZero() {
		lastSeenStr = formatTimestamp(snapshot.LastSeenAt)
		lastSeenISO = snapshot.LastSeenAt.UTC().Format(time.RFC3339)
	}

	latencyLabel := "-"
	if snapshot.LatencyMS > 0 {
		latencyLabel = h.t(r, "nodes.latency_ms", "ms", snapshot.LatencyMS)
	}

	engineKind := "binary"
	if strings.EqualFold(snapshot.InstallMode, "docker") || snapshot.NodeType == pasarguardType {
		engineKind = "docker"
	}

	snapshotView := view.NodeSnapshotView{
		Name:           fallbackDisplay(snapshot.ServiceName),
		NodeType:       fallbackDisplay(snapshot.NodeType),
		TypeLabel:      fallbackDisplay(snapshot.NodeType),
		EngineKind:     engineKind,
		InstallMode:    fallbackDisplay(snapshot.InstallMode),
		Version:        fallbackDisplay(snapshot.Version),
		HealthStatus:   fallbackDisplay(snapshot.HealthStatus),
		IsRunning:      isRunning,
		IsDegraded:     isDegraded,
		IsRestarting:   isRestarting,
		IsStopped:      isStopped,
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
		LastSeenAt:     lastSeenStr,
		LastSeenISO:    lastSeenISO,
		LatencyMS:      snapshot.LatencyMS,
		LatencyLabel:   latencyLabel,
		ActionsEnabled: actionsEnabled,
	}
	if provider, ok := ProviderByType(h.providers, snapshot.NodeType); ok {
		snapshotView.TypeLabel = provider.DisplayName()
		for _, action := range provider.Actions() {
			snapshotView.Actions = append(snapshotView.Actions, view.NodeActionView{
				Key:    action.Key,
				Label:  h.translateActionLabel(r, action.Label),
				Icon:   action.Icon,
				Danger: action.Danger,
			})
		}
	}
	return snapshotView
}

func (h *Handlers) loadStreamView(r *http.Request, streamID string, serverID int64) (view.CommandStreamView, string, string) {
	if streamID == "" || h.deps.CommandStreams == nil {
		return view.CommandStreamView{}, "", ""
	}

	snapshot, ok := h.deps.CommandStreams.Get(streamID)
	if !ok || snapshot.ServerID != serverID {
		return view.CommandStreamView{}, "error", h.t(r, "nodes.flash_stream_missing")
	}

	flashKind := "success"
	flashMessage := h.t(r, "nodes.flash_stream_running")
	switch snapshot.Status {
	case commandstream.StatusFailed:
		flashKind = "error"
		flashMessage = h.t(r, "nodes.flash_stream_failed")
	case commandstream.StatusCompleted:
		flashMessage = h.t(r, "nodes.flash_stream_complete")
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
	fleet := buildNodeFleetView(state.snapshots)
	fleet.InstallingCount = h.installs.countRunning(server.ID)
	fleet.EmptyState = emptyStateFor(state.snapshots, state.hasSnapshot)
	page.NodeFleet = fleet

	collection := state.collection
	collection.HasSnapshot = state.hasSnapshot
	page.NodeCollection = collection
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

func emptyStateFor(snapshots []view.NodeSnapshotView, hasSnapshot bool) string {
	if len(snapshots) > 0 {
		return ""
	}
	if hasSnapshot {
		return "none"
	}
	return "nosnap"
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
		UpdatedAt:          formatTimestamp(server.UpdatedAt),
	}
}

// buildNodeFleetView aggregates per-node snapshots into fleet counters, excluding
// any placeholder rows.
func buildNodeFleetView(snapshots []view.NodeSnapshotView) view.NodeFleetView {
	fleet := view.NodeFleetView{}
	portSet := make(map[string]struct{})
	var totalUptime, countUptime int

	addPort := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || p == "-" {
			return
		}
		portSet[p] = struct{}{}
	}

	for _, s := range snapshots {
		if isPlaceholderNodeType(s.NodeType) && s.NodeType != "" {
			continue
		}
		fleet.Total++
		switch strings.TrimSpace(s.NodeType) {
		case pasarguardType:
			fleet.PasarGuardCount++
		case rebeccaType:
			fleet.RebeccaCount++
		}

		switch strings.ToLower(strings.TrimSpace(s.HealthStatus)) {
		case "running", "active", "healthy":
			fleet.Running++
		case "degraded", "unhealthy", "restarting", "activating", "starting":
			fleet.Degraded++
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
	if nodeName == "" && formErrors["node_name"] == "" {
		nodeName = defaults.NodeName
	}
	servicePort := strings.TrimSpace(input.ServicePort)
	if servicePort == "" && formErrors["service_port"] == "" {
		servicePort = defaults.ServicePort
	}
	apiPort := strings.TrimSpace(input.APIPort)
	if apiPort == "" && formErrors["api_port"] == "" {
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
		Action:          nodesURL(server.ID) + "/install",
		Engines:         h.deployEngines(r),
		NodeType:        nodeType,
		NodeName:        nodeName,
		ServicePort:     servicePort,
		APIPort:         apiPort,
		Protocol:        protocol,
		APIKey:          strings.TrimSpace(input.APIKey),
		SANEntries:      strings.TrimSpace(input.SANEntries),
		Channel:         channel,
		Channels:        channels,
		ConfirmOverride: input.ConfirmOverride,
		Enabled:         enabled,
		Errors:          formErrors,
	}
}

func (h *Handlers) queryFlash(r *http.Request) (string, string) {
	switch r.URL.Query().Get("flash") {
	case "nodes-no-credentials":
		return "error", h.t(r, "nodes.flash_no_credentials")
	case "nodes-invalid-action":
		return "error", h.t(r, "nodes.flash_invalid_action")
	case "nodes-unknown-node":
		return "error", h.t(r, "nodes.flash_unknown_node")
	case "nodes-install-expired":
		return "error", h.t(r, "nodes.flash_install_expired")
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

func (h *Handlers) validateForm(r *http.Request, input FormInput, server servers.Server, deps module.Dependencies) (ValidationErrors, time.Duration, time.Duration) {
	validationErrors := ValidationErrors{}

	connectTimeout, err := parseDurationField(input.ConnectTimeout, deps.Config.SSH.ConnectTimeout)
	if err != nil {
		validationErrors["connect_timeout"] = h.t(r, "nodes.err_validate_connect_timeout")
	}

	commandTimeout, err := parseDurationField(input.CommandTimeout, deps.Config.SSH.CommandTimeout)
	if err != nil {
		validationErrors["command_timeout"] = h.t(r, "nodes.err_validate_command_timeout")
	}

	password := strings.TrimSpace(input.Password)
	privateKey := strings.TrimSpace(input.PrivateKey)
	switch server.AuthMode {
	case "password":
		if password == "" {
			validationErrors["password"] = h.t(r, "nodes.err_validate_password")
		}
	case "key":
		if privateKey == "" {
			validationErrors["private_key"] = h.t(r, "nodes.err_validate_private_key")
		}
	case "hybrid":
		if password == "" && privateKey == "" {
			validationErrors["password"] = h.t(r, "nodes.err_validate_hybrid")
			validationErrors["private_key"] = h.t(r, "nodes.err_validate_hybrid")
		}
	default:
		if password == "" && privateKey == "" {
			validationErrors["password"] = h.t(r, "nodes.err_validate_runtime_creds")
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
		Available:   len(probes) > 0,
		CollectedAt: formatTimestamp(collectedAt),
		Duration:    formatDuration(totalDuration),
		ProbeCount:  len(viewProbes),
		Probes:      viewProbes,
		Error:       strings.Join(errorsList, "\n"),
	}
}

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
		if settled && snap.Status == installStatusCompleted && stage == stageReady {
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
