package bulk

import (
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

// BulkWorkers exposes the pool size to tests without making it a public API.
const BulkWorkers = bulkWorkers

// NewTestHandlers wires an action handler and a job page handler around a
// shared job store, with an injectable command runner so tests never need a
// real SSH server.
func NewTestHandlers(deps module.Dependencies, serverRepo servers.Repository, runner commandRunner) (ActionHandler, JobPageHandler) {
	store := newJobStore()
	return newActionHandler(deps, serverRepo, runner, store), newJobPageHandler(deps, store)
}
