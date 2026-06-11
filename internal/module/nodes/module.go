package nodes

import (
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

type Module struct{}

func New() Module {
	return Module{}
}

func (Module) Name() string {
	return "nodes"
}

func (Module) RouteGroup() string {
	return "/servers/{id}/nodes"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil {
		mux.Handle("GET /servers/{id}/nodes", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			Title:       "Nodes",
			RouteGroup:  "/servers/{id}/nodes",
			Description: "This module requires a database runtime and SSH service to collect node discovery evidence.",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	repo := NewSQLRepository(deps.Database.SQL)
	detectors := DefaultDetectors()
	mux.Handle("GET /servers/{id}/nodes", NewPageHandler(deps, serverRepo, repo, detectors))
	mux.Handle("POST /servers/{id}/nodes", NewRefreshHandler(deps, serverRepo, repo, detectors))
}
