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
			"Database runtime unavailable",
			"The servers page cannot load because the database runtime is not available.",
		)
		page.ActiveNav = "/servers"
		if err := h.deps.Renderer.Render(w, http.StatusInternalServerError, page); err != nil {
			http.Error(w, "database runtime is not available", http.StatusInternalServerError)
		}
		return
	}

	servers, err := h.repo.List(r.Context())
	if err != nil {
		renderRepositoryError(w, r, h.deps, err, "Could not load servers", "The server registry could not be loaded from the persistence layer.")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))

	renderListPage(w, r, h.deps, servers, query, page, flashKind(r), flashMessage(r))
}
