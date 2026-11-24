package servers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

func wantsJSON(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

type NewHandler struct {
	deps module.Dependencies
}

func NewNewHandler(deps module.Dependencies) NewHandler {
	return NewHandler{deps: deps}
}

func (h NewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	renderFormPage(
		w, r,
		h.deps,
		http.StatusOK,
		"servers.form.title_new",
		"servers.form.new_description",
		NewFormViewData(DefaultFormInput(), ValidationErrors{}),
		"",
		"",
	)
}

type CreateHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewCreateHandler(deps module.Dependencies, repo Repository) CreateHandler {
	return CreateHandler{deps: deps, repo: repo}
}

func (h CreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	asJSON := wantsJSON(r)
	if err := r.ParseForm(); err != nil {
		if asJSON {
			loc := localizerFromRequest(r)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"ok":      false,
				"message": loc.T("servers.error.invalid_form_message"),
			})
			return
		}
		renderRepositoryError(w, r, h.deps, err, "servers.error.invalid_form_title", "servers.error.invalid_form_message")
		return
	}

	validated, validationErrors := ValidateForm(formInputFromRequest(r))
	if validationErrors.HasAny() {
		if asJSON {
			loc := localizerFromRequest(r)
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"ok":      false,
				"message": loc.T("servers.form.fix_fields"),
				"errors":  validationErrors,
			})
			return
		}
		renderFormPage(
			w, r,
			h.deps,
			http.StatusUnprocessableEntity,
			"servers.form.title_new",
			"servers.form.new_invalid_description",
			NewFormViewData(validated.Input, validationErrors),
			"error",
			"servers.form.fix_fields",
		)
		return
	}

	created, err := h.repo.Create(r.Context(), validated.Server)
	if err != nil {
		if asJSON {
			loc := localizerFromRequest(r)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok":      false,
				"message": loc.T("servers.error.create_message"),
			})
			return
		}
		renderRepositoryError(w, r, h.deps, err, "servers.error.create_title", "servers.error.create_message")
		return
	}

	// Detect the new server's country in the background. This must not block or
	// fail the create — the flag is filled in asynchronously (and by later
	// scheduler sweeps) over the server's own SSH connection.
	if h.deps.CountryResolver != nil {
		h.deps.CountryResolver.ResolveCountryAsync(created.ID)
	}

	redirectURL := "/servers?flash=created&id=" + formatID(created.ID)
	if asJSON {
		loc := localizerFromRequest(r)
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":       true,
			"id":       created.ID,
			"name":     created.Name,
			"host":     created.Host,
			"port":     created.Port,
			"redirect": redirectURL,
			"message":  loc.T("servers.flash.created"),
		})
		return
	}

	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

type TestConnectionHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewTestConnectionHandler(deps module.Dependencies, repo Repository) TestConnectionHandler {
	return TestConnectionHandler{deps: deps, repo: repo}
}

func (h TestConnectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	loc := localizerFromRequest(r)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"message": loc.T("servers.error.invalid_form_message"),
		})
		return
	}

	input := formInputFromRequest(r)
	// Name is not required to test an SSH connection while composing the form.
	if strings.TrimSpace(input.Name) == "" {
		input.Name = "ssh-test"
	}
	if strings.TrimSpace(input.AuthMode) == "" {
		input.AuthMode = AuthModeHybrid
	}
	if strings.TrimSpace(input.CredentialStrategy) == "" {
		input.CredentialStrategy = CredentialStrategyStored
	}

	validated, validationErrors := ValidateForm(input)
	delete(validationErrors, "name")
	delete(validationErrors, "tags")
	delete(validationErrors, "traffic_reset_day")
	if validationErrors.HasAny() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"ok":      false,
			"message": loc.T("servers.form.fix_fields"),
			"errors":  validationErrors,
		})
		return
	}

	if h.deps.SSH == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"error": loc.T("servers.form.test_ssh_unavailable"),
		})
		return
	}

	server := validated.Server
	if server.CredentialRef == "" &&
		(server.CredentialStrategy == CredentialStrategyStored || server.CredentialStrategy == CredentialStrategyRuntime) &&
		h.repo != nil {
		if rawID := strings.TrimSpace(r.FormValue("server_id")); rawID != "" {
			if id, err := strconv.ParseInt(rawID, 10, 64); err == nil && id > 0 {
				if existing, err := h.repo.GetByID(r.Context(), id); err == nil &&
					hasStoredSecret(existing.CredentialStrategy, existing.CredentialRef) {
					server.CredentialRef = existing.CredentialRef
				}
			}
		}
	}

	password, privateKey, keyPassphrase := ResolveCredentials(server)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	started := time.Now()
	result, err := h.deps.SSH.TestConnection(ctx, sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: 7 * time.Second,
	})
	elapsedMS := result.Duration.Milliseconds()
	if elapsedMS <= 0 {
		elapsedMS = time.Since(started).Milliseconds()
	}

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          false,
			"duration_ms": elapsedMS,
			"error":       err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"address":     result.RemoteAddress,
		"duration_ms": elapsedMS,
		"message":     loc.T("servers.form.test_ok", "address", result.RemoteAddress, "ms", strconv.FormatInt(elapsedMS, 10)),
	})
}

type EditHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewEditHandler(deps module.Dependencies, repo Repository) EditHandler {
	return EditHandler{deps: deps, repo: repo}
}

func (h EditHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		renderRepositoryError(w, r, h.deps, ErrNotFound, "servers.error.not_found_title", "servers.error.not_found_message")
		return
	}

	server, err := h.repo.GetByID(r.Context(), serverID)
	if err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.load_title", "servers.error.load_message_requested")
		return
	}

	renderFormPage(
		w, r,
		h.deps,
		http.StatusOK,
		"servers.form.title_edit",
		"servers.form.edit_description",
		EditFormViewData(server, ValidationErrors{}),
		"",
		"",
	)
}

type UpdateHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewUpdateHandler(deps module.Dependencies, repo Repository) UpdateHandler {
	return UpdateHandler{deps: deps, repo: repo}
}

func (h UpdateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		renderRepositoryError(w, r, h.deps, ErrNotFound, "servers.error.not_found_title", "servers.error.not_found_message")
		return
	}

	if err := r.ParseForm(); err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.invalid_form_title", "servers.error.invalid_form_message")
		return
	}

	existing, err := h.repo.GetByID(r.Context(), serverID)
	if err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.load_title", "servers.error.load_message_requested")
		return
	}

	validated, validationErrors := ValidateForm(formInputFromRequest(r))
	validated.Server.ID = serverID
	if validated.Server.CredentialRef == "" &&
		(validated.Server.CredentialStrategy == CredentialStrategyStored || validated.Server.CredentialStrategy == CredentialStrategyRuntime) &&
		hasStoredSecret(existing.CredentialStrategy, existing.CredentialRef) {
		validated.Server.CredentialRef = existing.CredentialRef
	}
	if validationErrors.HasAny() {
		form := EditFormViewData(validated.Server, validationErrors)
		form.Name = validated.Input.Name
		form.Host = validated.Input.Host
		form.Port = validated.Input.Port
		form.AuthMode = validated.Input.AuthMode
		form.Username = validated.Input.Username
		form.Tags = validated.Input.Tags
		form.Note = validated.Input.Note
		form.CredentialStrategy = validated.Input.CredentialStrategy
		form.CredentialRef = visibleCredentialRef(validated.Input.CredentialStrategy, validated.Input.CredentialRef)
		form.HasStoredCredential = hasStoredSecret(validated.Server.CredentialStrategy, validated.Server.CredentialRef)
		form.TrafficResetDay = validated.Input.TrafficResetDay
		renderFormPage(
			w, r,
			h.deps,
			http.StatusUnprocessableEntity,
			"servers.form.title_edit",
			"servers.form.edit_invalid_description",
			form,
			"error",
			"servers.form.fix_fields",
		)
		return
	}

	updated, err := h.repo.Update(r.Context(), validated.Server)
	if err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.update_title", "servers.error.update_message")
		return
	}

	// Host or credentials may have changed, so re-detect the country in the
	// background. Non-blocking and best-effort, exactly like the create path.
	if h.deps.CountryResolver != nil {
		h.deps.CountryResolver.ResolveCountryAsync(updated.ID)
	}

	http.Redirect(w, r, "/servers?flash=updated&id="+formatID(updated.ID), http.StatusSeeOther)
}

type DeleteHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewDeleteHandler(deps module.Dependencies, repo Repository) DeleteHandler {
	return DeleteHandler{deps: deps, repo: repo}
}

func (h DeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		renderRepositoryError(w, r, h.deps, ErrNotFound, "servers.error.not_found_title", "servers.error.not_found_message")
		return
	}

	if err := h.repo.Delete(r.Context(), serverID); err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.delete_title", "servers.error.delete_message")
		return
	}

	http.Redirect(w, r, "/servers?flash=deleted&id="+formatID(serverID), http.StatusSeeOther)
}

type ForgetHostKeyHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewForgetHostKeyHandler(deps module.Dependencies, repo Repository) ForgetHostKeyHandler {
	return ForgetHostKeyHandler{deps: deps, repo: repo}
}

func (h ForgetHostKeyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		renderRepositoryError(w, r, h.deps, ErrNotFound, "servers.error.not_found_title", "servers.error.not_found_message")
		return
	}

	server, err := h.repo.GetByID(r.Context(), serverID)
	if err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.load_title", "servers.error.load_message")
		return
	}

	port := server.Port
	if port <= 0 {
		port = 22
	}
	address := net.JoinHostPort(server.Host, strconv.Itoa(port))

	if h.deps.SSH != nil {
		if err := h.deps.SSH.ForgetHostKey(address); err != nil {
			renderRepositoryError(w, r, h.deps, err, "servers.error.forget_title", "servers.error.forget_message")
			return
		}
	}

	http.Redirect(w, r, "/servers?flash=host-key-forgotten&id="+formatID(serverID), http.StatusSeeOther)
}

func pathID(r *http.Request) (int64, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}

	return id, true
}

func formInputFromRequest(r *http.Request) FormInput {
	return FormInput{
		Name:               r.FormValue("name"),
		Host:               r.FormValue("host"),
		Port:               r.FormValue("port"),
		AuthMode:           r.FormValue("auth_mode"),
		Username:           r.FormValue("username"),
		Tags:               r.FormValue("tags"),
		Note:               r.FormValue("note"),
		CredentialStrategy: r.FormValue("credential_strategy"),
		CredentialRef:      r.FormValue("credential_ref"),
		TrafficResetDay:    r.FormValue("traffic_reset_day"),
	}
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}
