package system

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
	return "system"
}

func (Module) RouteGroup() string {
	return "/servers/{id}/system"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil {
		mux.Handle("GET /servers/{id}/system", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			TitleKey:       "system.title",
			RouteGroup:     "/servers/{id}/system",
			DescriptionKey: "module.placeholder.system",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	factRepo := NewSQLRepository(deps.Database.SQL)
	mux.Handle("GET /servers/{id}/system", NewPageHandler(deps, serverRepo, factRepo))
	mux.Handle("POST /servers/{id}/system", NewRefreshHandler(deps, serverRepo, factRepo))
}
