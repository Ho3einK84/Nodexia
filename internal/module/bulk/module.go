package bulk

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
	return "bulk"
}

func (Module) RouteGroup() string {
	return "/servers/bulk"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil {
		mux.Handle("POST /servers/bulk", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			Title:       "Bulk actions",
			RouteGroup:  "/servers/bulk",
			Description: "Bulk server actions need the database and SSH runtime before they can run.",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	mux.Handle("POST /servers/bulk", NewActionHandler(deps, serverRepo))
}
