package files

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Ho3einK84/Nodexia/internal/humanize"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// OpsHandler serves the JSON and streaming file API (list, stat, read, write,
// upload, mkdir, rename, move, copy, delete, chmod, symlink, compress, extract,
// search, archive_download) driven by the browser without page reloads.
//
// Requests carry intent in the query string and the CSRF synchronizer token in
// the X-CSRF-Token header. That lets the CSRF middleware validate the token
// without consuming the request body, so uploads can be streamed straight to
// SFTP via a multipart reader instead of being buffered. Credentials are
// runtime-only: resolved from stored credentials when present, otherwise read
// from the fields the client submits.
type OpsHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
}

func NewOpsHandler(deps module.Dependencies, serverRepo servers.Repository) OpsHandler {
	return OpsHandler{deps: deps, serverRepo: serverRepo}
}

const (
	// maxCredFieldBytes caps each non-file multipart field.
	maxCredFieldBytes = 1 << 20 // 1 MiB
	// maxFileUploadBytes caps a single streamed SFTP upload request body.
	maxFileUploadBytes = 512 << 20 // 512 MiB
	// maxOpJSONBytes caps non-upload JSON / form request bodies.
	maxOpJSONBytes = 4 << 20 // 4 MiB
	// maxEditableBytes caps text files opened/saved in the in-browser editor.
	maxEditableBytes = 2 << 20 // 2 MiB
	// maxTailBytes caps log-tail preview slices.
	maxTailBytes = 256 << 10 // 256 KiB
	// maxImagePreviewBytes caps base64 image previews returned in JSON.
	maxImagePreviewBytes = 5 << 20 // 5 MiB
)

type opPayload struct {
	Path          string   `json:"path"`
	Paths         []string `json:"paths"`
	Name          string   `json:"name"`
	Names         []string `json:"names"`
	Dest          string   `json:"dest"`
	Target        string   `json:"target"`
	Content       string   `json:"content"`
	Mode          string   `json:"mode"`
	Format        string   `json:"format"`
	Query         string   `json:"query"`
	Password      string   `json:"password"`
	PrivateKey    string   `json:"private_key"`
	KeyPassphrase string   `json:"key_passphrase"`
	Recursive     bool     `json:"recursive"`
	CreateOnly    bool     `json:"create_only"`
	Tail          bool     `json:"tail"`
	CalcSize      bool     `json:"calc_size"`
	Overwrite     bool     `json:"overwrite"`
	Duplicate     bool     `json:"duplicate"`
	SearchContent bool     `json:"search_content"`
	IncludeHidden bool     `json:"include_hidden"`
}

type fileEntryJSON struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Size       string `json:"size"`
	SizeBytes  int64  `json:"size_bytes"`
	Mode       string `json:"mode"`
	ModeOctal  string `json:"mode_octal"`
	UID        uint32 `json:"uid"`
	GID        uint32 `json:"gid"`
	IsSymlink  bool   `json:"is_symlink"`
	LinkTarget string `json:"link_target,omitempty"`
	LinkIsDir  bool   `json:"link_is_dir"`
	BrokenLink bool   `json:"broken_link"`
	IsHidden   bool   `json:"is_hidden"`
	Ext        string `json:"ext,omitempty"`
	ModifiedAt string `json:"modified_at"`
	ModUnix    int64  `json:"mod_unix"`
}

type fileVFSJSON struct {
	Available   bool   `json:"available"`
	Total       string `json:"total"`
	Used        string `json:"used"`
	Free        string `json:"free"`
	UsedPercent int    `json:"used_percent"`
	Severity    string `json:"severity"`
}

type fileListingJSON struct {
	Path           string          `json:"path"`
	Parent         string          `json:"parent"`
	HomeDir        string          `json:"home_dir,omitempty"`
	DirCount       int             `json:"dir_count"`
	FileCount      int             `json:"file_count"`
	HiddenCount    int             `json:"hidden_count"`
	TotalSize      string          `json:"total_size"`
	TotalSizeBytes int64           `json:"total_size_bytes"`
	VFS            fileVFSJSON     `json:"vfs"`
	Entries        []fileEntryJSON `json:"entries"`
}

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
	case "list":
		h.handleList(w, r, server)
	case "stat":
		h.handleStat(w, r, server)
	case "read":
		h.handleRead(w, r, server)
	case "write":
		h.handleWrite(w, r, server)
	case "upload":
		h.handleUpload(w, r, server)
	case "mkdir":
		h.handleMkdir(w, r, server)
	case "rename":
		h.handleRename(w, r, server)
	case "move":
		h.handleMove(w, r, server)
	case "copy":
		h.handleCopy(w, r, server)
	case "delete":
		h.handleDelete(w, r, server)
	case "chmod":
		h.handleChmod(w, r, server)
	case "symlink":
		h.handleSymlink(w, r, server)
	case "compress":
		h.handleCompress(w, r, server)
	case "extract":
		h.handleExtract(w, r, server)
	case "search":
		h.handleSearch(w, r, server)
	case "archive_download":
		h.handleOpsArchiveDownload(w, r, server)
	default:
		writeJSONError(w, http.StatusBadRequest, "Unknown file action.")
	}
}

func (h OpsHandler) handleList(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}

	targetPathStr := strings.TrimSpace(payload.Path)
	if targetPathStr == "" {
		targetPathStr = "~"
	} else if targetPathStr != "~" && !strings.HasPrefix(targetPathStr, "~/") {
		norm, perr := normalizeRemotePath(targetPathStr, defaultRemotePath(server))
		if perr != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(perr))
			return
		}
		targetPathStr = norm
	}

	conn := h.connectionFromPayload(server, payload)
	listing, err := h.deps.SSH.ListDirectory(r.Context(), conn, targetPathStr)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}

	lv := listingViewFromResult(listing)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"listing": listingJSONFromView(lv),
	})
}

func (h OpsHandler) handleStat(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	target, err := normalizeRemotePath(payload.Path, defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(err))
		return
	}

	conn := h.connectionFromPayload(server, payload)
	stat, err := h.deps.SSH.StatPath(r.Context(), conn, target, payload.CalcSize)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}

	kind := "file"
	if stat.IsDir {
		kind = "directory"
	} else if stat.IsSymlink {
		kind = "symlink"
	}

	sizeHuman := formatInt64Bytes(stat.Size)
	dirTotalHuman := formatInt64Bytes(stat.DirTotalBytes)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"stat": map[string]any{
			"name":             stat.Name,
			"path":             stat.Path,
			"kind":             kind,
			"is_dir":           stat.IsDir,
			"is_symlink":       stat.IsSymlink,
			"link_target":      stat.LinkTarget,
			"link_is_dir":      stat.LinkIsDir,
			"broken_link":      stat.BrokenLink,
			"size_bytes":       stat.Size,
			"size":             sizeHuman,
			"mode":             stat.Mode,
			"mode_octal":       stat.ModeOctal,
			"uid":              stat.UID,
			"gid":              stat.GID,
			"modified_at":      formatTimestamp(stat.ModifiedAt),
			"access_time":      formatTimestamp(stat.AccessTime),
			"dir_files":        stat.DirFiles,
			"dir_dirs":         stat.DirDirs,
			"dir_total_bytes":  stat.DirTotalBytes,
			"dir_total_size":   dirTotalHuman,
			"size_truncated":   stat.SizeTruncated,
			"size_calculated":  payload.CalcSize,
		},
	})
}

func (h OpsHandler) handleRead(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	target, ok := targetPath(payload.Path)
	if !ok {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select a valid file to view.")
		return
	}

	conn := h.connectionFromPayload(server, payload)
	ext := strings.ToLower(path.Ext(target))
	isImageExt := isPreviewImageExt(ext)

	if isImageExt && !payload.Tail {
		res, err := h.deps.SSH.ReadFileContent(r.Context(), conn, target, maxImagePreviewBytes, false)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, friendlyError(err))
			return
		}
		if res.Truncated {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true,
				"file": map[string]any{
					"path":        res.Path,
					"name":        res.Name,
					"size_bytes":  res.Size,
					"size":        formatInt64Bytes(res.Size),
					"mode":        res.Mode,
					"mode_octal":  res.ModeOctal,
					"modified_at": formatTimestamp(res.ModifiedAt),
					"uid":         res.UID,
					"gid":         res.GID,
					"is_image":    true,
					"is_binary":   true,
					"editable":    false,
					"truncated":   true,
				},
			})
			return
		}
		mimeType := imageMimeType(ext)
		dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(res.Content)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"file": map[string]any{
				"path":        res.Path,
				"name":        res.Name,
				"size_bytes":  res.Size,
				"size":        formatInt64Bytes(res.Size),
				"mode":        res.Mode,
				"mode_octal":  res.ModeOctal,
				"modified_at": formatTimestamp(res.ModifiedAt),
				"uid":         res.UID,
				"gid":         res.GID,
				"is_image":    true,
				"is_binary":   false,
				"editable":    false,
				"truncated":   false,
				"mime_type":   mimeType,
				"data_url":    dataURL,
			},
		})
		return
	}

	limit := int64(maxEditableBytes)
	if payload.Tail {
		limit = maxTailBytes
	}
	res, err := h.deps.SSH.ReadFileContent(r.Context(), conn, target, limit, payload.Tail)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}

	contentStr := ""
	lineCount := 0
	if !res.IsBinary {
		contentStr = string(res.Content)
		if contentStr != "" {
			lineCount = strings.Count(contentStr, "\n") + 1
		}
	}

	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "text/plain; charset=utf-8"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"file": map[string]any{
			"path":        res.Path,
			"name":        res.Name,
			"size_bytes":  res.Size,
			"size":        formatInt64Bytes(res.Size),
			"mode":        res.Mode,
			"mode_octal":  res.ModeOctal,
			"modified_at": formatTimestamp(res.ModifiedAt),
			"uid":         res.UID,
			"gid":         res.GID,
			"is_image":    false,
			"is_binary":   res.IsBinary,
			"editable":    !res.IsBinary && !res.Truncated && !res.IsTail,
			"truncated":   res.Truncated,
			"is_tail":     res.IsTail,
			"content":     contentStr,
			"line_count":  lineCount,
			"mime_type":   mimeType,
		},
	})
}

func (h OpsHandler) handleWrite(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "File content exceeds the 2 MiB editor limit.")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	if int64(len(payload.Content)) > maxEditableBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "File content exceeds the 2 MiB editor limit.")
		return
	}

	target, ok := targetPath(payload.Path)
	if !ok {
		writeJSONError(w, http.StatusUnprocessableEntity, "Choose a valid file path.")
		return
	}

	conn := h.connectionFromPayload(server, payload)
	res, err := h.deps.SSH.WriteFileContent(r.Context(), conn, target, []byte(payload.Content), payload.CreateOnly)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"file": map[string]any{
			"name":        res.Name,
			"path":        res.Path,
			"size_bytes":  res.Size,
			"size":        formatInt64Bytes(res.Size),
			"mode":        res.Mode,
			"mode_octal":  res.ModeOctal,
			"modified_at": formatTimestamp(res.ModifiedAt),
		},
	})
}

// handleUpload streams a single uploaded file to SFTP. The multipart body must
// carry any credential fields and the target "path" (plus optional
// "relative_path" for folder uploads) before the "file" part so the connection
// can be built before the bytes start flowing.
func (h OpsHandler) handleUpload(w http.ResponseWriter, r *http.Request, server servers.Server) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	r.Body = http.MaxBytesReader(w, r.Body, maxFileUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Expected a multipart upload.")
		return
	}

	var dir, relPathRaw, password, privateKey, passphrase string

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "Upload exceeds maximum allowed size.")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "Could not read upload stream.")
			return
		}

		if part.FileName() == "" {
			value := readLimited(part, maxCredFieldBytes)
			_ = part.Close()
			switch part.FormName() {
			case "path":
				dir = value
			case "relative_path":
				relPathRaw = value
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
			_ = part.Close()
			writeJSONError(w, http.StatusUnprocessableEntity, "Invalid destination path.")
			return
		}

		var remotePath, displayName string
		var createParents bool
		if strings.TrimSpace(relPathRaw) != "" {
			cleanRel, rerr := sanitizeUploadRelativePath(relPathRaw)
			if rerr != nil {
				_ = part.Close()
				writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(rerr))
				return
			}
			remotePath = path.Join(targetDir, cleanRel)
			displayName = path.Base(cleanRel)
			createParents = strings.Contains(cleanRel, "/")
		} else {
			base, nerr := sanitizeUploadName(part.FileName())
			if nerr != nil {
				_ = part.Close()
				writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(nerr))
				return
			}
			remotePath = path.Join(targetDir, base)
			displayName = base
		}

		conn := h.buildConnection(server, password, privateKey, passphrase)
		written, err := h.deps.SSH.UploadFileWithOptions(r.Context(), conn, remotePath, part, createParents)
		_ = part.Close()
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, friendlyError(err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"name": displayName,
			"path": remotePath,
			"size": written,
		})
		return
	}

	writeJSONError(w, http.StatusBadRequest, "No file found in the upload.")
}

func (h OpsHandler) handleMkdir(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}

	parent, err := normalizeRemotePath(payload.Path, defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid parent path.")
		return
	}

	var rel string
	if payload.Recursive {
		rel, err = sanitizeRelativeSubpath(payload.Name)
	} else {
		rel, err = validateBaseName(payload.Name)
	}
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(err))
		return
	}
	target := path.Join(parent, rel)

	conn := h.connectionFromPayload(server, payload)
	if err := h.deps.SSH.MakeDirectoryAll(r.Context(), conn, target, payload.Recursive); err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": path.Base(target), "path": target})
}

func (h OpsHandler) handleRename(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	oldPath, ok := targetPath(payload.Path)
	if !ok {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select a valid item to rename.")
		return
	}
	base, err := validateBaseName(payload.Name)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(err))
		return
	}
	newPath := path.Join(path.Dir(oldPath), base)
	if newPath == oldPath {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": base, "path": newPath})
		return
	}

	conn := h.connectionFromPayload(server, payload)
	if err := h.deps.SSH.RenamePath(r.Context(), conn, oldPath, newPath); err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": base, "path": newPath})
}

func (h OpsHandler) handleMove(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	sources := cleanTargetPaths(payload)
	if len(sources) == 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select at least one valid item to move.")
		return
	}
	destDir, err := normalizeRemotePath(payload.Dest, defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid destination directory.")
		return
	}

	conn := h.connectionFromPayload(server, payload)
	res, err := h.deps.SSH.MovePaths(r.Context(), conn, sources, destDir, payload.Overwrite)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeBatchResult(w, res)
}

func (h OpsHandler) handleCopy(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	sources := cleanTargetPaths(payload)
	if len(sources) == 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select at least one valid item to copy.")
		return
	}
	destDir, err := normalizeRemotePath(payload.Dest, defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid destination directory.")
		return
	}

	conn := h.connectionFromPayload(server, payload)
	res, err := h.deps.SSH.CopyPaths(r.Context(), conn, sources, destDir, payload.Duplicate)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeBatchResult(w, res)
}

func (h OpsHandler) handleDelete(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	targets := cleanTargetPaths(payload)
	if len(targets) == 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select a valid item to delete.")
		return
	}

	conn := h.connectionFromPayload(server, payload)
	if len(targets) == 1 && len(payload.Paths) == 0 {
		if err := h.deps.SSH.RemovePath(r.Context(), conn, targets[0], payload.Recursive); err != nil {
			writeJSONError(w, http.StatusBadGateway, friendlyError(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": targets[0], "succeeded": targets, "failed": []any{}})
		return
	}

	res, err := h.deps.SSH.BatchRemovePaths(r.Context(), conn, targets, payload.Recursive)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeBatchResult(w, res)
}

func (h OpsHandler) handleChmod(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	targets := cleanTargetPaths(payload)
	if len(targets) == 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select at least one valid item.")
		return
	}
	mode, err := parseChmodMode(payload.Mode)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(err))
		return
	}

	conn := h.connectionFromPayload(server, payload)
	res, err := h.deps.SSH.ChmodPaths(r.Context(), conn, targets, mode, payload.Recursive)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeBatchResult(w, res)
}

func (h OpsHandler) handleSymlink(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	parent, err := normalizeRemotePath(payload.Path, defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid parent directory.")
		return
	}
	linkName, err := validateBaseName(payload.Name)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(err))
		return
	}
	target := strings.TrimSpace(payload.Target)
	if target == "" || strings.ContainsRune(target, '\x00') {
		writeJSONError(w, http.StatusUnprocessableEntity, "Enter a valid symlink target.")
		return
	}

	linkPath := path.Join(parent, linkName)
	conn := h.connectionFromPayload(server, payload)
	if err := h.deps.SSH.CreateSymlink(r.Context(), conn, target, linkPath); err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": linkName, "path": linkPath, "target": target})
}

func (h OpsHandler) handleCompress(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	baseDir, err := normalizeRemotePath(payload.Path, defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid base directory.")
		return
	}
	names := make([]string, 0, len(payload.Names))
	for _, rawName := range payload.Names {
		cleanName, nerr := validateBaseName(rawName)
		if nerr != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(nerr))
			return
		}
		names = append(names, cleanName)
	}
	if len(names) == 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select at least one item to compress.")
		return
	}

	format := normalizeArchiveFormat(payload.Format)
	archiveName, err := validateBaseName(payload.Name)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, validationErrorMessage(err))
		return
	}
	ext := ".zip"
	if format == "tar.gz" {
		ext = ".tar.gz"
	}
	lowerName := strings.ToLower(archiveName)
	if !strings.HasSuffix(lowerName, ext) && (format != "tar.gz" || !strings.HasSuffix(lowerName, ".tgz")) {
		archiveName += ext
	}

	conn := h.connectionFromPayload(server, payload)
	entry, err := h.deps.SSH.CompressPaths(r.Context(), conn, baseDir, names, archiveName, format)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"name": entry.Name,
		"path": entry.Path,
		"size": entry.Size,
	})
}

func (h OpsHandler) handleExtract(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	archivePath, ok := targetPath(payload.Path)
	if !ok {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select a valid archive file.")
		return
	}
	destDir := strings.TrimSpace(payload.Dest)
	if destDir == "" {
		destDir = path.Dir(archivePath)
	}
	destDir, err = normalizeRemotePath(destDir, path.Dir(archivePath))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid destination directory.")
		return
	}

	conn := h.connectionFromPayload(server, payload)
	res, err := h.deps.SSH.ExtractArchive(r.Context(), conn, archivePath, destDir)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"dest_dir":        res.DestDir,
		"extracted_files": res.ExtractedFiles,
		"extracted_dirs":  res.ExtractedDirs,
		"bytes_written":   res.BytesWritten,
		"skipped_entries": res.SkippedEntries,
	})
}

func (h OpsHandler) handleSearch(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	rootDir, err := normalizeRemotePath(payload.Path, defaultRemotePath(server))
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid search directory.")
		return
	}
	query := strings.TrimSpace(payload.Query)
	if query == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "Enter a search query.")
		return
	}

	conn := h.connectionFromPayload(server, payload)
	res, err := h.deps.SSH.SearchFiles(r.Context(), conn, rootDir, query, payload.SearchContent, payload.IncludeHidden)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, friendlyError(err))
		return
	}

	matches := make([]map[string]any, 0, len(res.Matches))
	for _, m := range res.Matches {
		matches = append(matches, map[string]any{
			"name":        m.Entry.Name,
			"path":        m.Entry.Path,
			"dir":         path.Dir(m.Entry.Path),
			"rel_path":    m.RelPath,
			"is_dir":      m.Entry.IsDir,
			"is_symlink":  m.Entry.IsSymlink,
			"size_bytes":  m.Entry.Size,
			"size":        formatInt64Bytes(m.Entry.Size),
			"mode":        m.Entry.Mode,
			"mode_octal":  m.Entry.ModeOctal,
			"modified_at": formatTimestamp(m.Entry.ModifiedAt),
			"line":        m.Line,
			"snippet":     m.Snippet,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"root_dir":  res.RootDir,
		"query":     res.Query,
		"truncated": res.Truncated,
		"scanned":   res.Scanned,
		"matches":   matches,
	})
}

func (h OpsHandler) handleOpsArchiveDownload(w http.ResponseWriter, r *http.Request, server servers.Server) {
	payload, err := parseOpPayload(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Could not parse request.")
		return
	}
	targets := cleanTargetPaths(payload)
	if len(targets) == 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, "Select one or more items to download.")
		return
	}

	format := normalizeArchiveFormat(payload.Format)
	archiveName := sanitizeDownloadName(payload.Name)
	if archiveName == "download.bin" || archiveName == "" {
		if len(targets) == 1 {
			archiveName = sanitizeDownloadName(path.Base(targets[0]))
		} else {
			archiveName = "files"
		}
	}
	ext := ".zip"
	contentType := "application/zip"
	if format == "tar.gz" {
		ext = ".tar.gz"
		contentType = "application/gzip"
	}
	if !strings.HasSuffix(strings.ToLower(archiveName), ext) {
		archiveName += ext
	}

	conn := h.connectionFromPayload(server, payload)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition(archiveName))
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = h.deps.SSH.StreamArchive(r.Context(), conn, targets, format, w)
}

// ── Helpers ────────────────────────────────────────────────────────────────

func parseOpPayload(w http.ResponseWriter, r *http.Request) (opPayload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOpJSONBytes)
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "application/json") {
		var p opPayload
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&p); err != nil {
			return opPayload{}, err
		}
		return p, nil
	}

	if err := r.ParseForm(); err != nil {
		return opPayload{}, err
	}
	p := opPayload{
		Path:          r.PostFormValue("path"),
		Name:          r.PostFormValue("name"),
		Dest:          r.PostFormValue("dest"),
		Target:        r.PostFormValue("target"),
		Content:       r.PostFormValue("content"),
		Mode:          r.PostFormValue("mode"),
		Format:        r.PostFormValue("format"),
		Query:         r.PostFormValue("query"),
		Password:      r.PostFormValue("password"),
		PrivateKey:    r.PostFormValue("private_key"),
		KeyPassphrase: r.PostFormValue("key_passphrase"),
		Recursive:     parseBoolField(r.PostFormValue("recursive")),
		CreateOnly:    parseBoolField(r.PostFormValue("create_only")),
		Tail:          parseBoolField(r.PostFormValue("tail")),
		CalcSize:      parseBoolField(r.PostFormValue("calc_size")),
		Overwrite:     parseBoolField(r.PostFormValue("overwrite")),
		Duplicate:     parseBoolField(r.PostFormValue("duplicate")),
		SearchContent: parseBoolField(r.PostFormValue("search_content")),
		IncludeHidden: parseBoolField(r.PostFormValue("include_hidden")),
	}
	p.Paths = parseStringSliceForm(r.PostForm["paths"])
	p.Names = parseStringSliceForm(r.PostForm["names"])
	return p, nil
}

func parseBoolField(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}

func parseStringSliceForm(vals []string) []string {
	if len(vals) == 0 {
		return nil
	}
	if len(vals) == 1 && strings.HasPrefix(strings.TrimSpace(vals[0]), "[") {
		var parsed []string
		if err := json.Unmarshal([]byte(vals[0]), &parsed); err == nil {
			return parsed
		}
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func collectArchivePathsFromForm(r *http.Request) []string {
	rawList := parseStringSliceForm(r.Form["paths"])
	if len(rawList) == 0 {
		if single := strings.TrimSpace(r.FormValue("path")); single != "" {
			rawList = []string{single}
		}
	}
	out := make([]string, 0, len(rawList))
	seen := map[string]bool{}
	for _, raw := range rawList {
		if t, ok := targetPath(raw); ok && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func cleanTargetPaths(p opPayload) []string {
	raw := make([]string, 0, len(p.Paths)+1)
	if len(p.Paths) > 0 {
		raw = append(raw, p.Paths...)
	} else if strings.TrimSpace(p.Path) != "" {
		raw = append(raw, p.Path)
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		if t, ok := targetPath(item); ok && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func writeBatchResult(w http.ResponseWriter, res sshclient.BatchOpResult) {
	if len(res.Succeeded) == 0 && len(res.Failed) > 0 {
		msg := friendlyError(errors.New(res.Failed[0].Error))
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":        false,
			"error":     msg,
			"succeeded": res.Succeeded,
			"failed":    res.Failed,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"succeeded": res.Succeeded,
		"failed":    res.Failed,
	})
}

func (h OpsHandler) connectionFromPayload(server servers.Server, p opPayload) sshclient.ConnectionRequest {
	return h.buildConnection(server, p.Password, p.PrivateKey, p.KeyPassphrase)
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

func listingJSONFromView(v view.FileListingView) fileListingJSON {
	entries := make([]fileEntryJSON, 0, len(v.Entries))
	for _, e := range v.Entries {
		entries = append(entries, fileEntryJSON{
			Name:       e.Name,
			Path:       e.Path,
			Kind:       e.Kind,
			Size:       e.Size,
			SizeBytes:  e.SizeBytes,
			Mode:       e.Mode,
			ModeOctal:  e.ModeOctal,
			UID:        e.UID,
			GID:        e.GID,
			IsSymlink:  e.IsSymlink,
			LinkTarget: e.LinkTarget,
			LinkIsDir:  e.LinkIsDir,
			BrokenLink: e.BrokenLink,
			IsHidden:   e.IsHidden,
			Ext:        e.Ext,
			ModifiedAt: e.ModifiedAt,
			ModUnix:    e.ModUnix,
		})
	}
	return fileListingJSON{
		Path:           v.Path,
		Parent:         v.Parent,
		HomeDir:        v.HomeDir,
		DirCount:       v.DirCount,
		FileCount:      v.FileCount,
		HiddenCount:    v.HiddenCount,
		TotalSize:      v.TotalSize,
		TotalSizeBytes: v.TotalSizeBytes,
		VFS: fileVFSJSON{
			Available:   v.VFS.Available,
			Total:       v.VFS.Total,
			Used:        v.VFS.Used,
			Free:        v.VFS.Free,
			UsedPercent: v.VFS.UsedPercent,
			Severity:    v.VFS.Severity,
		},
		Entries: entries,
	}
}

func formatInt64Bytes(v int64) string {
	return humanize.Bytes(
		v,
		humanize.WithPrecision(1),
		humanize.WithIntegerBytes(),
		humanize.WithoutNonPositiveFallback(),
	)
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
		return "", errors.New("enter a name")
	}
	if strings.ContainsAny(name, "/\x00") {
		return "", errors.New("name cannot contain slashes")
	}
	if name == "." || name == ".." {
		return "", errors.New("choose a different name")
	}
	return name, nil
}

// sanitizeRelativeSubpath validates a relative directory path such as
// "configs/nginx/sites-enabled" where every segment must be a safe base name.
func sanitizeRelativeSubpath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", errors.New("enter a name")
	}
	parts := strings.Split(raw, "/")
	cleaned := make([]string, 0, len(parts))
	for _, seg := range parts {
		valid, err := validateBaseName(seg)
		if err != nil {
			return "", err
		}
		cleaned = append(cleaned, valid)
	}
	return strings.Join(cleaned, "/"), nil
}

func sanitizeUploadRelativePath(raw string) (string, error) {
	return sanitizeRelativeSubpath(raw)
}

// sanitizeUploadName reduces a browser-supplied filename to a safe base segment,
// guarding against path traversal from clients that send a relative path.
func sanitizeUploadName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	return validateBaseName(name)
}

func parseChmodMode(raw string) (os.FileMode, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("enter an octal permission mode such as 0644 or 0755")
	}
	if len(raw) > 4 {
		return 0, errors.New("permission mode must be 3 or 4 octal digits (e.g. 0755)")
	}
	val, err := strconv.ParseUint(raw, 8, 32)
	if err != nil || val > 0o7777 {
		return 0, errors.New("enter a valid octal permission mode between 0000 and 7777")
	}
	mode := os.FileMode(val & 0o777)
	if val&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if val&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if val&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode, nil
}

func normalizeArchiveFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "tar.gz", "tgz", "targz":
		return "tar.gz"
	default:
		return "zip"
	}
}

func isPreviewImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico":
		return true
	default:
		return false
	}
}

func imageMimeType(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

func readLimited(r io.Reader, limit int64) string {
	data, _ := io.ReadAll(io.LimitReader(r, limit))
	return string(data)
}

// validationErrorMessage turns a composable Go error back into the standalone
// sentence expected by file-form fields and JSON error responses.
func validationErrorMessage(err error) string {
	msg := err.Error()
	if msg == "" {
		return ""
	}
	runes := []rune(msg)
	runes[0] = unicode.ToUpper(runes[0])
	msg = string(runes)
	switch msg[len(msg)-1] {
	case '.', '!', '?':
		return msg
	default:
		return msg + "."
	}
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
