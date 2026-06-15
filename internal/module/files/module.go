package files

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
	return "files"
}

func (Module) RouteGroup() string {
	return "/servers/{id}/files"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil {
		mux.Handle("GET /servers/{id}/files", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			TitleKey:       "files.title",
			RouteGroup:     "/servers/{id}/files",
			DescriptionKey: "module.placeholder.files",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	mux.Handle("GET /servers/{id}/files", NewPageHandler(deps, serverRepo))
	mux.Handle("POST /servers/{id}/files", NewActionHandler(deps, serverRepo))
	mux.Handle("POST /servers/{id}/files/ops", NewOpsHandler(deps, serverRepo))
}
