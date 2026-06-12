package bulk

// BulkWorkers exposes the pool size to tests without making it a public API.
const BulkWorkers = bulkWorkers

// NewActionHandlerWithRunner exposes the runner-injection constructor for tests.
var NewActionHandlerWithRunner = newActionHandlerWithRunner
