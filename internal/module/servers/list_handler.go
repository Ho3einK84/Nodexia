package servers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type ListHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewListHandler(deps module.Dependencies, repo Repository) ListHandler {
	return ListHandler{
		deps: deps,
		repo: repo,
	}
}

func (h ListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.deps.Database == nil || h.deps.Database.SQL == nil {
		page := view.NewErrorPageData(
			h.deps.Config,
			r,
			http.StatusInternalServerError,
			"",
			"",
		)
		page.ErrorTitle = page.T("servers.error.db_unavailable_title")
		page.ErrorMessage = page.T("servers.error.db_unavailable_message")
		page.Title = page.ErrorTitle
		page.Description = page.ErrorMessage
		page.ActiveNav = "/servers"
		if err := h.deps.Renderer.Render(w, http.StatusInternalServerError, page); err != nil {
			http.Error(w, "database runtime is not available", http.StatusInternalServerError)
		}
		return
	}

	servers, err := h.repo.List(r.Context())
	if err != nil {
		renderRepositoryError(w, r, h.deps, err, "servers.error.load_servers_title", "servers.error.load_servers_message")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))

	renderListPage(w, r, h.deps, servers, query, page, flashKind(r), flashMessage(r))
}
