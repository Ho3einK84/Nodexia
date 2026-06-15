package servers

import (
	"net"
	"net/http"
	"strconv"

	"github.com/Ho3einK84/Nodexia/internal/module"
)

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
	if err := r.ParseForm(); err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.invalid_form_title", "servers.error.invalid_form_message")
		return
	}

	validated, validationErrors := ValidateForm(formInputFromRequest(r))
	if validationErrors.HasAny() {
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
		renderRepositoryError(w, r, h.deps, err, "servers.error.create_title", "servers.error.create_message")
		return
	}

	// Detect the new server's country in the background. This must not block or
	// fail the create — the flag is filled in asynchronously (and by later
	// scheduler sweeps) over the server's own SSH connection.
	if h.deps.CountryResolver != nil {
		h.deps.CountryResolver.ResolveCountryAsync(created.ID)
	}

	http.Redirect(w, r, "/servers?flash=created&id="+formatID(created.ID), http.StatusSeeOther)
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

	validated, validationErrors := ValidateForm(formInputFromRequest(r))
	validated.Server.ID = serverID
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
		form.CredentialRef = validated.Input.CredentialRef
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
	}
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}
