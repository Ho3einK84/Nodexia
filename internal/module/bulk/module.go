package bulk

import (
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

// Module owns the in-memory job store so the POST handler (which starts jobs)
// and the GET handler (which renders their progress) share it.
type Module struct {
	jobs *jobStore
}

func New() Module {
	return Module{jobs: newJobStore()}
}

func (Module) Name() string {
	return "bulk"
}

func (Module) RouteGroup() string {
	return "/servers/bulk"
}

func (m Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil {
		placeholder := module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			TitleKey:       "bulk.title",
			RouteGroup:     "/servers/bulk",
			DescriptionKey: "module.placeholder.bulk",
		})
		mux.Handle("POST /servers/bulk", placeholder)
		mux.Handle("GET /servers/bulk/jobs/{job}", placeholder)
		mux.Handle("GET /servers/bulk/jobs/{job}/events", placeholder)
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	mux.Handle("POST /servers/bulk", newActionHandler(deps, serverRepo, deps.SSH, m.jobs))
	mux.Handle("GET /servers/bulk/jobs/{job}", newJobPageHandler(deps, m.jobs))
	mux.Handle("GET /servers/bulk/jobs/{job}/events", newJobEventsHandler(deps, m.jobs))
}
