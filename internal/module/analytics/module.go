package analytics

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
	return "analytics"
}

func (Module) RouteGroup() string {
	return "/analytics"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil {
		mux.Handle("GET /analytics", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			TitleKey:       "nav.analytics",
			RouteGroup:     "/analytics",
			DescriptionKey: "module.placeholder.analytics",
		}))
		return
	}

	repo := NewSQLRepository(deps.Database.SQL)
	forecastSvc := NewForecastService()
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	limitsAdmin := NewLimitsAdminHandler(deps, repo)

	mux.Handle("GET /analytics", NewGlobalHandler(deps, repo))
	mux.HandleFunc("GET /analytics/limits", limitsAdmin.Page)
	mux.HandleFunc("POST /analytics/limits", limitsAdmin.SetGlobal)
	mux.HandleFunc("POST /analytics/limits/tags", limitsAdmin.SetTag)
	mux.HandleFunc("POST /analytics/limits/tags/delete", limitsAdmin.DeleteTag)
	mux.Handle("GET /servers/{id}/analytics", NewPageHandler(deps, serverRepo, repo))
	mux.Handle("GET /servers/{id}/analytics/data", NewDataHandler(deps, serverRepo, repo))
	mux.Handle("GET /servers/{id}/analytics/forecast", NewForecastHandler(deps, serverRepo, repo, forecastSvc))
	mux.Handle("POST /servers/{id}/analytics/limit", NewLimitHandler(deps, serverRepo, repo))
}
