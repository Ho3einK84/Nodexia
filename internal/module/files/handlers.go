package files

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
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
}

type ActionHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
}

type FormInput struct {
	Intent         string
	Path           string
	Password       string
	PrivateKey     string
	KeyPassphrase  string
	ConnectTimeout string
}

type ValidationErrors map[string]string

func NewPageHandler(deps module.Dependencies, serverRepo servers.Repository) PageHandler {
	return PageHandler{
		deps:       deps,
		serverRepo: serverRepo,
	}
}

func NewActionHandler(deps module.Dependencies, serverRepo servers.Repository) ActionHandler {
	return ActionHandler{
		deps:       deps,
		serverRepo: serverRepo,
	}
}

func (h PageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	form := defaultFormView(h.deps, server, hasStoredCreds)
	listing := view.FileListingView{}
	download := view.FileDownloadView{}
	flashKind := ""
	flashMessage := ""

	if hasStoredCreds {
		password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
		connectTimeout := h.deps.Config.SSH.ConnectTimeout

		remotePath := defaultRemotePath(server)
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

		result, err := h.deps.SSH.ListDirectory(r.Context(), req, remotePath)
		if err == nil {
			listing = listingViewFromResult(result)
			form.Path = remotePath
		} else {
			download = view.FileDownloadView{
				Available: true,
				Path:      remotePath,
				Error:     err.Error(),
			}
			flashKind = "error"
			flashMessage = "Directory listing failed."
		}
	}

	renderPage(
		w,
		r,
		h.deps,
		http.StatusOK,
		server,
		form,
		listing,
		download,
		flashKind,
		flashMessage,
	)
}

func (h ActionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid file request", "The submitted file action could not be parsed.")
		return
	}

	form := formInputFromRequest(r, h.deps, server)
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
	validationErrors, connectTimeout, remotePath := validateForm(form, server, h.deps)
	if validationErrors.HasAny() {
		renderPage(
			w,
			r,
			h.deps,
			http.StatusUnprocessableEntity,
			server,
			formViewFromInput(form, validationErrors, hasStoredCreds),
			view.FileListingView{},
			view.FileDownloadView{},
			"error",
			"Please fix the highlighted fields before continuing.",
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
	case "browse":
		listing, err := h.deps.SSH.ListDirectory(r.Context(), request, remotePath)
		if err != nil {
			renderPage(
				w,
				r,
				h.deps,
				http.StatusBadGateway,
				server,
				defaultFormViewWithPath(h.deps, server, remotePath, hasStoredCreds),
				view.FileListingView{},
				view.FileDownloadView{Available: true, Path: remotePath, Error: err.Error()},
				"error",
				"Directory listing failed.",
			)
			return
		}

		renderPage(
			w,
			r,
			h.deps,
			http.StatusOK,
			server,
			defaultFormViewWithPath(h.deps, server, remotePath, hasStoredCreds),
			listingViewFromResult(listing),
			view.FileDownloadView{},
			"success",
			"Directory listing loaded successfully.",
		)
	case "download":
		file, err := h.deps.SSH.OpenFile(r.Context(), request, remotePath)
		if err != nil {
			renderPage(
				w,
				r,
				h.deps,
				http.StatusBadGateway,
				server,
				defaultFormViewWithPath(h.deps, server, remotePath, hasStoredCreds),
				view.FileListingView{},
				view.FileDownloadView{Available: true, Path: remotePath, Error: err.Error()},
				"error",
				"File download could not start.",
			)
			return
		}
		defer file.Content.Close()

		downloadName := sanitizeDownloadName(file.Name)
		contentType := mime.TypeByExtension(path.Ext(downloadName))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", downloadName))
		w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
		if _, err := io.Copy(w, file.Content); err != nil {
			return
		}
	default:
		hasStoredCreds := servers.HasStoredCredentials(server)
		formErrors := ValidationErrors{"intent": "Choose a valid file action."}
		renderPage(
			w,
			r,
			h.deps,
			http.StatusUnprocessableEntity,
			server,
			formViewFromInput(form, formErrors, hasStoredCreds),
			view.FileListingView{},
			view.FileDownloadView{},
			"error",
			"Choose a valid file action and submit the form again.",
		)
	}
}

func loadServer(w http.ResponseWriter, r *http.Request, deps module.Dependencies, serverRepo servers.Repository) (servers.Server, bool) {
	serverID, ok := pathID(r)
	if !ok {
		httperrors.RenderPage(w, r, deps, servers.ErrNotFound, "/servers", "Server not found", "The requested server record does not exist.")
		return servers.Server{}, false
	}

	server, err := serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, deps, err, "/servers", "Could not load server", "The file browser could not load the selected server.")
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
	form view.FileFormView,
	listing view.FileListingView,
	download view.FileDownloadView,
	flashKind string,
	flashMessage string,
) {
	page := view.NewPageData(deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Files"
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-files"
	page.PageTitle = "File browser for " + server.Name
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = "Browse remote directories over SFTP and download files with runtime credentials only."
	if deps.Database != nil {
		page.MigrationCount = deps.Database.MigrationCount()
	}
	page.FileTarget = view.FileTargetView{
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
		form.Action = "/servers/" + formatID(server.ID) + "/files"
	}
	page.FileForm = form
	page.FileListing = listing
	page.FileDownload = download
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage
	page.PageStyles = []string{"/static/files.css"}
	page.PageScripts = []string{"/static/files.js"}

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render file browser page", http.StatusInternalServerError)
	}
}

func defaultFormView(deps module.Dependencies, server servers.Server, hasStoredCreds bool) view.FileFormView {
	return defaultFormViewWithPath(deps, server, defaultRemotePath(server), hasStoredCreds)
}

func defaultFormViewWithPath(deps module.Dependencies, server servers.Server, remotePath string, hasStoredCreds ...bool) view.FileFormView {
	stored := len(hasStoredCreds) > 0 && hasStoredCreds[0]
	return view.FileFormView{
		Action:                     "/servers/" + formatID(server.ID) + "/files",
		Path:                       remotePath,
		ConnectTimeout:             deps.Config.SSH.ConnectTimeout.String(),
		StoredCredentialsAvailable: stored,
		RefreshURL:                 filesURL(server.ID),
		Errors:                     map[string]string{},
	}
}

func filesURL(serverID int64) string {
	return "/servers/" + formatID(serverID) + "/files"
}

func formInputFromRequest(r *http.Request, deps module.Dependencies, server servers.Server) FormInput {
	return FormInput{
		Intent:         strings.TrimSpace(r.FormValue("intent")),
		Path:           fallbackString(strings.TrimSpace(r.FormValue("path")), defaultRemotePath(server)),
		Password:       r.FormValue("password"),
		PrivateKey:     r.FormValue("private_key"),
		KeyPassphrase:  r.FormValue("key_passphrase"),
		ConnectTimeout: fallbackString(strings.TrimSpace(r.FormValue("connect_timeout")), deps.Config.SSH.ConnectTimeout.String()),
	}
}

func formViewFromInput(input FormInput, validationErrors ValidationErrors, hasStoredCreds ...bool) view.FileFormView {
	stored := len(hasStoredCreds) > 0 && hasStoredCreds[0]
	return view.FileFormView{
		Path:                       input.Path,
		ConnectTimeout:             input.ConnectTimeout,
		StoredCredentialsAvailable: stored,
		Errors:                     validationErrors,
	}
}

func validateForm(input FormInput, server servers.Server, deps module.Dependencies) (ValidationErrors, time.Duration, string) {
	validationErrors := ValidationErrors{}

	connectTimeout, err := parseDurationField(input.ConnectTimeout, deps.Config.SSH.ConnectTimeout)
	if err != nil {
		validationErrors["connect_timeout"] = "Enter a valid connection timeout such as 10s or 30s."
	}

	remotePath, err := normalizeRemotePath(input.Path, defaultRemotePath(server))
	if err != nil {
		validationErrors["path"] = err.Error()
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

	switch input.Intent {
	case "browse", "download":
	default:
		validationErrors["intent"] = "Choose a valid file action."
	}

	return validationErrors, connectTimeout, remotePath
}

func listingViewFromResult(listing sshclient.DirectoryListing) view.FileListingView {
	items := make([]view.FileEntryView, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		kind := "file"
		if entry.IsDir {
			kind = "directory"
		}
		modUnix := int64(0)
		if !entry.ModifiedAt.IsZero() {
			modUnix = entry.ModifiedAt.Unix()
		}
		items = append(items, view.FileEntryView{
			Name:       entry.Name,
			Path:       entry.Path,
			Kind:       kind,
			Size:       formatSize(entry.Size),
			SizeBytes:  entry.Size,
			Mode:       entry.Mode,
			ModifiedAt: formatTimestamp(entry.ModifiedAt),
			ModUnix:    modUnix,
		})
	}

	return view.FileListingView{
		Available: true,
		Path:      listing.Path,
		Parent:    parentPath(listing.Path),
		Entries:   items,
	}
}

func normalizeRemotePath(rawPath string, fallback string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if strings.Contains(rawPath, "\x00") {
		return "", errors.New("Enter a valid remote path without null bytes.")
	}
	if rawPath == "" {
		rawPath = fallback
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = path.Join(fallback, rawPath)
	}
	cleaned := path.Clean(rawPath)
	if cleaned == "." {
		cleaned = fallback
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned, nil
}

func defaultRemotePath(server servers.Server) string {
	username := strings.TrimSpace(server.Username)
	if username == "" || username == "root" {
		return "/root"
	}
	return "/home/" + username
}

func parentPath(remotePath string) string {
	if remotePath == "/" {
		return ""
	}
	parent := path.Dir(remotePath)
	if parent == "." || parent == remotePath {
		return "/"
	}
	return parent
}

func sanitizeDownloadName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\"", "")
	if name == "" {
		return "download.bin"
	}
	return name
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

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func pathID(r *http.Request) (int64, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func (v ValidationErrors) HasAny() bool {
	return len(v) > 0
}
