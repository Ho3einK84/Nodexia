package files

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

// OpsHandler serves the JSON file-mutation API (upload, mkdir, rename, delete)
// that the browser drives with fetch/XHR so the listing page never reloads for
// an action. Browsing and downloading remain plain form posts on the page
// handler; only state-changing operations live here.
//
// Requests carry intent and _csrf_token in the query string. That lets the CSRF
// middleware validate the token without consuming the request body, so uploads
// can be streamed straight to SFTP via a multipart reader instead of being
// buffered. Credentials are runtime-only: resolved from stored credentials when
// present, otherwise read from the fields the client submits.
type OpsHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
}

func NewOpsHandler(deps module.Dependencies, serverRepo servers.Repository) OpsHandler {
	return OpsHandler{deps: deps, serverRepo: serverRepo}
}

// maxCredFieldBytes caps each non-file multipart field. Private keys are a few
// KiB at most; this stops a malicious client from buffering a huge "field".
const maxCredFieldBytes = 1 << 20 // 1 MiB

func (h OpsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "Server not found.")
		return
	}
	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "Server not found.")
		return
	}

	// intent comes from the query string so multipart uploads are not parsed
	// (and thus not buffered) before we get to stream them.
	switch r.URL.Query().Get("intent") {
	case "upload":
		h.handleUpload(w, r, server)
	case "mkdir":
		h.handleMkdir(w, r, server)
	case "rename":
		h.handleRename(w, r, server)
	case "delete":
		h.handleDelete(w, r, server)
	default:
		writeJSONError(w, http.StatusBadRequest, "Unknown file action.")
	}
}

// handleUpload streams a single uploaded file to SFTP. The multipart body must
// carry any credential fields and the target "path" before the "file" part so
// the connection can be built before the bytes start flowing.
func (h OpsHandler) handleUpload(w http.ResponseWriter, r *http.Request, server servers.Server) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Expected a multipart upload.")
		return
	}

	var dir, password, privateKey, passphrase string

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Could not read upload stream.")
			return
		}

		if part.FileName() == "" {
			value := readLimited(part, maxCredFieldBytes)
			_ = part.Close()
			switch part.FormName() {
			case "path":
				dir = value
			case "password":
				password = value
			case "private_key":
				privateKey = value
			case "key_passphrase":
				passphrase = value
			}
			continue
		}

		// First file part: resolve the destination and stream it through.
		targetDir, perr := normalizeRemotePath(dir, defaultRemotePath(server))
		if perr != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "Invalid destination path.")
			return
		}
		base, nerr := sanitizeUploadName(part.FileName())
		if nerr != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, nerr.Error())
			return
		}
		remotePath := path.Join(targetDir, base)

		conn := h.buildConnection(server, password, privateKey, passphrase)
		written, err := h.deps.SSH.UploadFile(r.Context(), conn, remotePath, part)
		_ = part.Close()
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, friendlyError(err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"name": base,
			"path": remotePath,
			"size": written,
		})
		return
	}

	writeJSONError(w, http.StatusBadRequest, "No file found in the upload.")
}

func (h OpsHandler) handleMkdir(w http.ResponseWriter, r *http.Request, server servers.Server) {
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	base, err := validateBaseName(r.PostFormValue("name"))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	parent, err := normalizeRemotePath(r.PostFormValue("path"), defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid parent path.")
		return
	}
	target := path.Join(parent, base)

	conn := h.connectionFromForm(server, r)
	if err := h.deps.SSH.MakeDirectory(r.Context(), conn, target); err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": base, "path": target})
}

func (h OpsHandler) handleRename(w http.ResponseWriter, r *http.Request, server servers.Server) {
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	oldPath, ok := targetPath(r.PostFormValue("path"))
	if !ok {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select a valid item to rename.")
		return
	}
	base, err := validateBaseName(r.PostFormValue("name"))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	newPath := path.Join(path.Dir(oldPath), base)
	if newPath == oldPath {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": base, "path": newPath})
		return
	}

	conn := h.connectionFromForm(server, r)
	if err := h.deps.SSH.RenamePath(r.Context(), conn, oldPath, newPath); err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": base, "path": newPath})
}

func (h OpsHandler) handleDelete(w http.ResponseWriter, r *http.Request, server servers.Server) {
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	target, ok := targetPath(r.PostFormValue("path"))
	if !ok {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select a valid item to delete.")
		return
	}
	recursive := r.PostFormValue("recursive") == "true"

	conn := h.connectionFromForm(server, r)
	if err := h.deps.SSH.RemovePath(r.Context(), conn, target, recursive); err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": target})
}

// ── Helpers ────────────────────────────────────────────────────────────────

func (h OpsHandler) connectionFromForm(server servers.Server, r *http.Request) sshclient.ConnectionRequest {
	return h.buildConnection(
		server,
		r.PostFormValue("password"),
		r.PostFormValue("private_key"),
		r.PostFormValue("key_passphrase"),
	)
}

// buildConnection assembles a runtime connection request, preferring stored
// credentials and falling back to whatever the client supplied for this action.
func (h OpsHandler) buildConnection(server servers.Server, password, privateKey, passphrase string) sshclient.ConnectionRequest {
	if servers.HasStoredCredentials(server) {
		storedPassword, storedKey, storedPassphrase := servers.ResolveCredentials(server)
		if strings.TrimSpace(password) == "" {
			password = storedPassword
		}
		if strings.TrimSpace(privateKey) == "" {
			privateKey = storedKey
		}
		if strings.TrimSpace(passphrase) == "" {
			passphrase = storedPassphrase
		}
	}
	return sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  passphrase,
		ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
	}
}

// targetPath cleans an absolute remote path and refuses the filesystem root so
// a stray request can never delete or move "/".
func targetPath(raw string) (string, bool) {
	cleaned, err := normalizeRemotePath(raw, "/")
	if err != nil || cleaned == "" || cleaned == "/" {
		return "", false
	}
	return cleaned, true
}

// validateBaseName ensures a user-supplied name is a single path segment.
func validateBaseName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("Enter a name.")
	}
	if strings.ContainsAny(name, "/\x00") {
		return "", errors.New("Name cannot contain slashes.")
	}
	if name == "." || name == ".." {
		return "", errors.New("Choose a different name.")
	}
	return name, nil
}

// sanitizeUploadName reduces a browser-supplied filename to a safe base segment,
// guarding against path traversal from clients that send a relative path.
func sanitizeUploadName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	return validateBaseName(name)
}

func readLimited(r io.Reader, limit int64) string {
	data, _ := io.ReadAll(io.LimitReader(r, limit))
	return string(data)
}

// friendlyError strips the internal "sshclient:" prefixes from an error so the
// surfaced JSON message reads cleanly in the UI.
func friendlyError(err error) string {
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "sshclient: ", "")
	if msg == "" {
		return "The operation failed."
	}
	runes := []rune(msg)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}
	return msg
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}
