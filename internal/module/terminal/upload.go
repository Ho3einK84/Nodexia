package terminal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

const maxTerminalUploadBytes = 256 << 20 // 256 MiB

type uploadHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	sessions   *terminalSessionHub
}

func newUploadHandler(deps module.Dependencies, serverRepo servers.Repository, sessions *terminalSessionHub) uploadHandler {
	return uploadHandler{
		deps:       deps,
		serverRepo: serverRepo,
		sessions:   sessions,
	}
}

var safeFileNameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func sanitizeUploadFileName(raw string) string {
	base := filepath.Base(filepath.Clean(raw))
	if base == "." || base == "/" || base == "\\" || base == "" {
		base = "file.bin"
	}
	cleaned := safeFileNameRe.ReplaceAllString(base, "_")
	if cleaned == "" || cleaned == "." {
		cleaned = "file.bin"
	}
	return cleaned
}

func (h uploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverID, ok := pathServerID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Invalid server ID"})
		return
	}

	username := middleware.GetAuthenticatedUser(r.Context())
	// Do NOT call r.FormValue() before r.MultipartReader(): parsing the body
	// prematurely consumes the multipart stream and bypasses MaxBytesReader.
	ticketID := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if ticketID == "" {
		ticketID = strings.TrimSpace(r.Header.Get("X-Terminal-Ticket"))
	}

	var connReq sshclient.ConnectionRequest
	var foundConn bool

	// Strategy 1: Active live terminal session
	if ticketID != "" && h.sessions != nil {
		if session, exists := h.sessions.get(ticketID); exists {
			if session.serverID == serverID && (username == "" || session.username == username) {
				connReq = session.req
				foundConn = true
			}
		}
	}

	// Strategy 2: Stored credentials fallback
	if !foundConn {
		server, err := h.serverRepo.GetByID(r.Context(), serverID)
		if err == nil && servers.HasStoredCredentials(server) {
			pwd, pk, pp := servers.ResolveCredentials(server)
			connReq = sshclient.ConnectionRequest{
				Host:           server.Host,
				Port:           server.Port,
				Username:       server.Username,
				AuthMode:       server.AuthMode,
				Password:       pwd,
				PrivateKeyPEM:  pk,
				KeyPassphrase:  pp,
				ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
			}
			foundConn = true
		}
	}

	if !foundConn {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "No active terminal session or credentials available"})
		return
	}

	// Clear the HTTP server's default ReadTimeout/WriteTimeout so large
	// uploads (up to 256 MiB) are not aborted mid-stream.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	r.Body = http.MaxBytesReader(w, r.Body, maxTerminalUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Expected multipart form data"})
		return
	}

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("Failed reading upload stream: %v", err)})
			return
		}

		if part.FileName() == "" {
			_ = part.Close()
			continue
		}

		cleanName := sanitizeUploadFileName(part.FileName())
		suffix := randomUploadSuffix()
		remoteFileName := fmt.Sprintf("nodexia_%s_%s", suffix, cleanName)
		remotePath := path.Join("/tmp", remoteFileName)

		written, uploadErr := h.deps.SSH.UploadFileExclusive(r.Context(), connReq, remotePath, part, 0o600)
		_ = part.Close()
		if uploadErr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"ok":    false,
				"error": fmt.Sprintf("Upload to %s failed: %v", remotePath, uploadErr),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"name": cleanName,
			"path": remotePath,
			"size": written,
		})
		return
	}

	writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "No file found in request"})
}

func randomUploadSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
