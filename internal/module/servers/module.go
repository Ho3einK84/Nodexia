package servers

import (
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/module"
)

type Module struct{}

func New() Module {
	return Module{}
}

func (Module) Name() string {
	return "servers"
}

func (Module) RouteGroup() string {
	return "/servers"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil {
		mux.Handle("GET /servers", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			TitleKey:       "servers.title",
			RouteGroup:     "/servers",
			DescriptionKey: "module.placeholder.servers",
		}))
		return
	}

	repo := NewSQLRepository(deps.Database.SQL)
	mux.Handle("GET /servers", NewListHandler(deps, repo))
	mux.Handle("GET /servers/new", NewNewHandler(deps))
	mux.Handle("POST /servers", NewCreateHandler(deps, repo))
	mux.Handle("GET /servers/{id}/edit", NewEditHandler(deps, repo))
	mux.Handle("POST /servers/{id}/edit", NewUpdateHandler(deps, repo))
	mux.Handle("POST /servers/{id}/delete", NewDeleteHandler(deps, repo))
	mux.Handle("POST /servers/{id}/forget-host-key", NewForgetHostKeyHandler(deps, repo))
}
