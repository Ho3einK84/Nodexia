// Package terminal provides the in-browser interactive SSH terminal.
//
// # Overview
//
// The terminal page uses xterm.js (vendored) over a WebSocket to give users a
// live PTY-backed shell.  This is the one place Nodexia uses client-side JS
// beyond progressive enhancement — xterm.js cannot be server-rendered.
//
// # WebSocket protocol (JSON-only framing)
//
// Client → Server:
//
//	{"type":"input","data":"<utf-8 string>"}
//	{"type":"resize","cols":<int>,"rows":<int>}
//	{"type":"heartbeat"}                          // ~30 s; echoed back
//
// Server → Client:
//
//	{"type":"output","data":"<utf-8 string>"}
//	{"type":"error","message":"<string>"}
//	{"type":"status","state":"connected"}         // sent once on accept
//	{"type":"heartbeat"}                          // echo of a client heartbeat
//
// In addition to the JSON heartbeat (which lets the client display round-trip
// latency), the server sends a protocol-level WebSocket ping every
// wsPingInterval to detect and tear down zombie connections.
//
// Unknown types are silently ignored server-side.
//
// # Credential flow
//
// STORED strategy: terminal can start immediately from the GET handler.
// RUNTIME strategy: a CSRF-protected POST collects credentials, builds a
// one-time ticket, and renders the xterm page.  Credentials are never persisted,
// logged, or placed in a URL.
//
// # Ticket lifecycle
//
// POST → create ticket (30 s TTL) → render terminal page (ticket id in
// data-ticket HTML attr) → JS opens WS → WS handler consumes ticket (single-use)
// → start PTY shell.  Ticket id is in the WS query string (?ticket=) for the
// upgrade request only; the actual credentials stay in the in-memory store.
//
// # Session limit
//
// At most maxTerminalSessionsPerUser concurrent sessions per authenticated user.
package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	cwebsocket "github.com/coder/websocket"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

const (
	maxTerminalSessionsPerUser = 3

	// wsWriteTimeout is the per-frame write deadline; if the client is too slow
	// the session is terminated rather than buffering output in memory.
	wsWriteTimeout = 5 * time.Second

	// maxInputFrameBytes caps the size of a single client→server input frame.
	maxInputFrameBytes = 16 * 1024

	// wsOutputChunkBytes is the maximum number of bytes forwarded per WS frame.
	wsOutputChunkBytes = 32 * 1024

	// wsPingInterval is how often the server sends a protocol-level WebSocket
	// ping. A missed pong (no reply within wsWriteTimeout) means the connection
	// is a zombie — most often a silently dropped mobile/NAT link — and the
	// session is torn down instead of lingering with a live SSH shell behind it.
	wsPingInterval = 30 * time.Second
)

// ── Page handler ──────────────────────────────────────────────────────────────

type pageHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
}

func newPageHandler(deps module.Dependencies, serverRepo servers.Repository) pageHandler {
	return pageHandler{deps: deps, serverRepo: serverRepo}
}

func (h pageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathServerID(r)
	if !ok {
		httperrors.RenderPage(w, r, h.deps, servers.ErrNotFound, "/servers", "Server not found", "")
		return
	}

	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load server", "")
		return
	}

	initCmd := sanitizeInitCommand(r.URL.Query().Get("init"))
	form := view.TerminalFormView{
		Action:                     terminalURL(serverID),
		ConnectTimeout:             h.deps.Config.SSH.ConnectTimeout.String(),
		StoredCredentialsAvailable: servers.HasStoredCredentials(server),
		InitCommand:                initCmd,
		Errors:                     map[string]string{},
	}

	renderTerminalPage(w, r, h.deps, server, form, "", initCmd)
}

// ── POST handler (credential collection + ticket creation) ────────────────────

type postHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
}

func newPostHandler(deps module.Dependencies, serverRepo servers.Repository) postHandler {
	return postHandler{deps: deps, serverRepo: serverRepo}
}

func (h postHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathServerID(r)
	if !ok {
		httperrors.RenderPage(w, r, h.deps, servers.ErrNotFound, "/servers", "Server not found", "")
		return
	}

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid request", "")
		return
	}

	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load server", "")
		return
	}

	hasCreds := servers.HasStoredCredentials(server)

	initCmd := sanitizeInitCommand(r.FormValue("init"))
	password := r.FormValue("password")
	privateKey := r.FormValue("private_key")
	keyPassphrase := r.FormValue("key_passphrase")
	connectTimeoutStr := strings.TrimSpace(r.FormValue("connect_timeout"))

	if hasCreds {
		p, pk, pp := servers.ResolveCredentials(server)
		if strings.TrimSpace(password) == "" {
			password = p
		}
		if strings.TrimSpace(privateKey) == "" {
			privateKey = pk
		}
		if strings.TrimSpace(keyPassphrase) == "" {
			keyPassphrase = pp
		}
	}

	// Minimal validation — the SSH dial will reject bad credentials; we only
	// gate on obviously missing values to give a quick UX error.
	formErrors := map[string]string{}
	switch server.AuthMode {
	case "password":
		if strings.TrimSpace(password) == "" && !hasCreds {
			formErrors["password"] = "Enter the SSH password for this session."
		}
	case "key":
		if strings.TrimSpace(privateKey) == "" && !hasCreds {
			formErrors["private_key"] = "Paste the SSH private key for this session."
		}
	case "hybrid":
		if strings.TrimSpace(password) == "" && strings.TrimSpace(privateKey) == "" && !hasCreds {
			formErrors["password"] = "Provide a password or private key."
			formErrors["private_key"] = "Provide a private key or password."
		}
	default:
		if strings.TrimSpace(password) == "" && strings.TrimSpace(privateKey) == "" && !hasCreds {
			formErrors["password"] = "Provide SSH credentials for this session."
		}
	}

	if len(formErrors) > 0 {
		form := view.TerminalFormView{
			Action:                     terminalURL(serverID),
			ConnectTimeout:             connectTimeoutStr,
			Password:                   password,
			PrivateKey:                 privateKey,
			StoredCredentialsAvailable: hasCreds,
			InitCommand:                initCmd,
			Errors:                     formErrors,
		}
		renderTerminalPage(w, r, h.deps, server, form, "", initCmd)
		return
	}

	connectTimeout := h.deps.Config.SSH.ConnectTimeout
	if connectTimeoutStr != "" {
		if d, err := time.ParseDuration(connectTimeoutStr); err == nil && d > 0 {
			connectTimeout = d
		}
	}

	req := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: connectTimeout,
	}

	ticketID := h.deps.TerminalTickets.Create(serverID, req)
	renderTerminalPage(w, r, h.deps, server, view.TerminalFormView{}, ticketID, initCmd)
}

// ── WebSocket handler ─────────────────────────────────────────────────────────

type wsHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
}

func newWSHandler(deps module.Dependencies, serverRepo servers.Repository) wsHandler {
	return wsHandler{deps: deps, serverRepo: serverRepo}
}

func (h wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Same-origin check (mirrors middleware.validateSameOriginRequest).
	if err := middleware.ValidateSameOriginRequest(r); err != nil {
		http.Error(w, "terminal: cross-origin WebSocket rejected", http.StatusForbidden)
		return
	}

	ticketID := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if ticketID == "" {
		http.Error(w, "terminal: missing ticket", http.StatusBadRequest)
		return
	}

	ticket, ok := h.deps.TerminalTickets.Consume(ticketID)
	if !ok {
		http.Error(w, "terminal: ticket invalid, expired, or already used", http.StatusUnauthorized)
		return
	}

	username := middleware.GetAuthenticatedUser(r.Context())
	if !h.deps.TerminalTickets.TryAcquireSession(username, maxTerminalSessionsPerUser) {
		// Reject before upgrading to keep the error response plain-text.
		http.Error(w, fmt.Sprintf("terminal: session limit reached (max %d)", maxTerminalSessionsPerUser), http.StatusTooManyRequests)
		return
	}

	conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{
		InsecureSkipVerify: true, // we already validated same-origin above
	})
	if err != nil {
		h.deps.TerminalTickets.ReleaseSession(username)
		return
	}
	defer func() {
		h.deps.TerminalTickets.ReleaseSession(username)
		h.deps.TerminalTickets.Release(ticketID)
		conn.Close(cwebsocket.StatusNormalClosure, "session ended")
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stdinR, stdinW := io.Pipe()
	defer stdinW.Close()

	resizeCh := make(chan sshclient.ResizeRequest, 8)

	wsOut := &wsOutputWriter{conn: conn, ctx: ctx}
	pio := sshclient.InteractiveIO{
		Stdin:  stdinR,
		Stdout: wsOut,
		Stderr: wsOut,
		Rows:   24,
		Cols:   80,
		Resize: resizeCh,
	}

	// Tell the client the session is live so its status chip can flip from
	// "connecting" to "connected" even before the shell emits its first byte.
	_ = wsOut.writeStatus("connected")

	// Protocol-level ping keepalive: detects a dead peer and tears the session
	// down by cancelling ctx (which stops the shell and the read loop).
	go h.pingKeepalive(ctx, cancel, conn)

	shellDone := make(chan error, 1)
	go func() {
		shellDone <- h.deps.SSH.OpenShell(ctx, ticket.Req, pio)
		cancel() // unblock the read loop when the shell exits
	}()

	// WS read loop runs until the client disconnects or the shell ends.
	_ = h.runReadLoop(ctx, conn, stdinW, resizeCh, wsOut)

	// Stop the shell if the read loop ended first, then wait for it.
	cancel()
	_ = stdinW.Close()
	shellErr := <-shellDone

	// Surface a real SSH/shell failure (auth rejected, host unreachable, PTY
	// refused, …) to the client.  ctx is already cancelled here, so the final
	// frame must use a fresh context — otherwise the write is dropped and the
	// user sees an unexplained disconnect with no reason.
	if shellErr != nil &&
		!errors.Is(shellErr, context.Canceled) &&
		!errors.Is(shellErr, context.DeadlineExceeded) {
		errCtx, errCancel := context.WithTimeout(context.Background(), wsWriteTimeout)
		_ = writeWSError(errCtx, conn, "ssh: "+shellErr.Error())
		errCancel()
	}
}

// pingKeepalive sends a protocol-level WebSocket ping every wsPingInterval and
// waits for the pong. A failed ping (timeout or write error) means the peer is
// gone, so it cancels the session context to tear everything down.
func (h wsHandler) pingKeepalive(ctx context.Context, cancel context.CancelFunc, conn *cwebsocket.Conn) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

// runReadLoop reads JSON frames from the WebSocket and routes them.
func (h wsHandler) runReadLoop(
	ctx context.Context,
	conn *cwebsocket.Conn,
	stdinW *io.PipeWriter,
	resizeCh chan<- sshclient.ResizeRequest,
	wsOut *wsOutputWriter,
) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols uint32 `json:"cols"`
			Rows uint32 `json:"rows"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "input":
			if len(msg.Data) > maxInputFrameBytes {
				continue // reject oversized frame
			}
			_, _ = stdinW.Write([]byte(msg.Data))
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				select {
				case resizeCh <- sshclient.ResizeRequest{Rows: msg.Rows, Cols: msg.Cols}:
				default: // drop if buffer full; next resize will apply
				}
			}
		case "heartbeat":
			// Echo so the client can measure round-trip latency. Best-effort —
			// a write failure here is surfaced by the next output write or ping.
			_ = wsOut.writeHeartbeat()
		}
		// Unknown types are silently ignored.
	}
}

// wsOutputWriter implements io.Writer by encoding each chunk as a JSON
// {"type":"output","data":"..."} frame and writing it to the WebSocket.
//
// It enforces a per-write timeout: if the client is too slow and a write
// exceeds wsWriteTimeout, Write returns an error which causes the SSH session
// to terminate (no unbounded buffering).
type wsOutputWriter struct {
	conn *cwebsocket.Conn
	ctx  context.Context
	mu   sync.Mutex
}

func (w *wsOutputWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		chunk := p
		if len(chunk) > wsOutputChunkBytes {
			chunk = p[:wsOutputChunkBytes]
		}
		if err := w.writeFrame(chunk); err != nil {
			return total - len(p), err
		}
		p = p[len(chunk):]
	}
	return total, nil
}

func (w *wsOutputWriter) writeFrame(data []byte) error {
	msg, err := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{"output", string(data)})
	if err != nil {
		return err
	}
	return w.writeRaw(msg)
}

// writeStatus emits a {"type":"status","state":...} control frame.
func (w *wsOutputWriter) writeStatus(state string) error {
	msg, err := json.Marshal(struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}{"status", state})
	if err != nil {
		return err
	}
	return w.writeRaw(msg)
}

// writeHeartbeat echoes a {"type":"heartbeat"} control frame.
func (w *wsOutputWriter) writeHeartbeat() error {
	msg, err := json.Marshal(struct {
		Type string `json:"type"`
	}{"heartbeat"})
	if err != nil {
		return err
	}
	return w.writeRaw(msg)
}

// writeRaw serialises all WebSocket writes through w.mu so output, status, and
// heartbeat frames never interleave (coder/websocket forbids concurrent writes).
func (w *wsOutputWriter) writeRaw(msg []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	writeCtx, cancel := context.WithTimeout(w.ctx, wsWriteTimeout)
	defer cancel()
	return w.conn.Write(writeCtx, cwebsocket.MessageText, msg)
}

func writeWSError(ctx context.Context, conn *cwebsocket.Conn, msg string) error {
	payload, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}{"error", msg})
	writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, cwebsocket.MessageText, payload)
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func renderTerminalPage(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	server servers.Server,
	form view.TerminalFormView,
	ticketID string,
	initCommand string,
) {
	page := view.NewPageData(deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("terminal.title")
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-terminal"
	page.PageTitle = page.T("terminal.page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("terminal.page_description", "server", server.Name)
	if deps.Database != nil {
		page.MigrationCount = deps.Database.MigrationCount()
	}
	page.TerminalTarget = view.TerminalTargetView{
		ID:                 server.ID,
		Name:               server.Name,
		Host:               server.Host,
		Port:               server.Port,
		Username:           server.Username,
		AuthMode:           server.AuthMode,
		CredentialStrategy: server.CredentialStrategy,
		WSURL:              wsURL(server.ID),
		InitCommand:        initCommand,
	}
	page.TerminalForm = form
	page.TerminalTicket = ticketID
	// Each asset URL carries a content-hash query string so a corrected
	// terminal.css/js can never be shadowed by a stale service-worker cache: when
	// a file changes, its URL changes, so the browser/SW must fetch the new copy
	// (no version bump required). See assets.StaticAssetVersion.
	page.PageStyles = []string{
		staticURL("xterm.min.css"),
		staticURL("terminal.css"),
	}
	// xterm.js core, then its addons, then the theme catalog and keybinding
	// handler, then terminal.js (which orchestrates them). All vendored locally —
	// the panel runs under a strict `script-src 'self'` CSP, so no CDN is used.
	page.PageScripts = []string{
		staticURL("xterm.min.js"),
		staticURL("xterm-addon-fit.min.js"),
		staticURL("xterm-addon-unicode11.min.js"),
		staticURL("xterm-addon-web-links.min.js"),
		staticURL("xterm-addon-search.min.js"),
		staticURL("xterm-addon-serialize.min.js"),
		staticURL("xterm-addon-webgl.min.js"),
		staticURL("xterm-addon-canvas.min.js"),
		staticURL("xterm-themes.js"),
		staticURL("terminal-keybindings.js"),
		staticURL("terminal.js"),
	}

	if err := deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render terminal page", http.StatusInternalServerError)
	}
}

func pathServerID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

// staticURL builds a /static URL for name with a content-hash cache-busting
// query string when one is available, so updated assets are never served stale
// from a browser or service-worker cache.
func staticURL(name string) string {
	if v := assets.StaticAssetVersion(name); v != "" {
		return "/static/" + name + "?v=" + v
	}
	return "/static/" + name
}

func terminalURL(serverID int64) string {
	return "/servers/" + strconv.FormatInt(serverID, 10) + "/terminal"
}

func wsURL(serverID int64) string {
	return "/servers/" + strconv.FormatInt(serverID, 10) + "/terminal/ws"
}

// maxInitCommandLen bounds the optional auto-run command carried from the
// command center.
const maxInitCommandLen = 512

// sanitizeInitCommand normalises the optional init command: single line only,
// control characters stripped (so it cannot inject extra shell input), and
// length-capped.  The command itself is not secret, but it must stay benign.
func sanitizeInitCommand(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == 0 {
			return -1
		}
		return r
	}, s)
	if len(s) > maxInitCommandLen {
		s = s[:maxInitCommandLen]
	}
	return strings.TrimSpace(s)
}
