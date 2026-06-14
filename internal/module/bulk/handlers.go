package bulk

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

const bulkWorkers = 5

// Bulk SSH actions run in a background job, so they can afford realistic
// timeouts: package upgrades routinely take minutes.  The global SSH command
// timeout (default 20 s) would abort them mid-flight.
const (
	bulkRebootTimeout = 2 * time.Minute
	bulkUpdateTimeout = 20 * time.Minute
	// Node actions fan out across every PasarGuard + Rebecca instance on a server
	// in one session, so they get their own generous budgets (an update pulls and
	// recreates Docker images for each instance).
	bulkNodeRestartTimeout = 10 * time.Minute
	bulkNodeUpdateTimeout  = 25 * time.Minute
)

// Non-interactive SSH exit codes returned by the sudo preamble and pkg-manager
// detection.  Both map to FAILED in the result summary.
const (
	exitSudoPassword   = 88
	exitUnsupportedPkg = 87
)

// rebootCommand runs a non-interactive reboot.  The sudo preamble checks
// whether we are already root; if not it tries sudo -n (no-password).  If
// that would require a password it prints a message and exits 88 so the
// worker can distinguish "sudo locked out" from an actual SSH error.
const rebootCommand = `if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else echo "sudo requires password" >&2; exit 88; fi; $SUDO reboot`

// updateCommand detects the package manager and runs a fully non-interactive
// upgrade.  Exits 87 if no recognised package manager is found.
const updateCommand = `if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else echo "sudo requires password" >&2; exit 88; fi; ` +
	`if command -v apt-get >/dev/null 2>&1; then ` +
	`DEBIAN_FRONTEND=noninteractive $SUDO apt-get update && DEBIAN_FRONTEND=noninteractive $SUDO apt-get -y upgrade; ` +
	`elif command -v dnf >/dev/null 2>&1; then $SUDO dnf -y upgrade; ` +
	`elif command -v yum >/dev/null 2>&1; then $SUDO yum -y update; ` +
	`elif command -v apk >/dev/null 2>&1; then $SUDO apk update && $SUDO apk upgrade; ` +
	`else echo "unsupported package manager" >&2; exit 87; fi`

// nodeActionPreamble mirrors the nodes module's sudo preamble (exit 88 when sudo
// would need a password) so bulk node actions behave exactly like a per-node
// action: already root, or passwordless sudo, or a clean 88.
const nodeActionPreamble = `if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else echo "sudo requires password" >&2; exit 88; fi; `

// nodeBulkCommand builds one SSH script that runs a node operation across every
// node instance on a server in a single session:
//
//   - PasarGuard: scans /opt/*/ for compose files referencing pasarguard/pg-node
//     (exactly like PasarGuardProvider.DiscoveryCommand), and runs the op via the
//     official `pg-node --name <name>` CLI (falling back to the per-instance CLI
//     name, mirroring ActionCommand) for each instance found.
//   - Rebecca: the single /opt/rebecca-node instance, run via `rebecca-node`.
//
// A provider with no instances is skipped silently (no output, no error). The
// script never aborts on a single instance's failure; it records one and exits
// non-zero at the end so the bulk result marks the server failed only when an
// action actually failed. pgOp is the pg-node operation (e.g. "restart -n");
// rebeccaInvoke is the full Rebecca invocation (it may use `$SUDO` and pipes).
func nodeBulkCommand(opLabel, pgOp, rebeccaInvoke string) string {
	return nodeActionPreamble +
		`fail=0; ` +
		`for dir in /opt/*/; do ` +
		`[ -f "$dir/docker-compose.yml" ] || continue; ` +
		`grep -Eqi "pasarguard|pg-node" "$dir/docker-compose.yml" 2>/dev/null || continue; ` +
		`name="${dir%/}"; name="${name##*/}"; ` +
		`if command -v pg-node >/dev/null 2>&1; then CLI="pg-node"; ` +
		`elif command -v "$name" >/dev/null 2>&1; then CLI="$name"; ` +
		`else echo "pg-node CLI not found for $name" >&2; fail=1; continue; fi; ` +
		`echo "== PasarGuard $name: ` + opLabel + ` =="; ` +
		`$SUDO "$CLI" --name "$name" ` + pgOp + ` </dev/null || fail=1; ` +
		`done; ` +
		`if [ -d /opt/rebecca-node ] || command -v rebecca-node >/dev/null 2>&1; then ` +
		`if command -v rebecca-node >/dev/null 2>&1; then ` +
		`echo "== Rebecca rebecca-node: ` + opLabel + ` =="; ` +
		rebeccaInvoke + ` || fail=1; ` +
		`else echo "rebecca-node CLI not found" >&2; fail=1; fi; ` +
		`fi; ` +
		`exit $fail`
}

// nodeRestartCommand restarts every node instance: pg-node restart -n, and
// rebecca-node restart.
func nodeRestartCommand() string {
	return nodeBulkCommand("restart", "restart -n", `$SUDO rebecca-node restart </dev/null`)
}

// nodeUpdateCommand updates every node instance: pg-node update --yes, and
// `yes | rebecca-node update` (Rebecca's update prompts for confirmation).
func nodeUpdateCommand() string {
	return nodeBulkCommand("update", "update --yes", `yes | $SUDO rebecca-node update`)
}

// commandRunner is a thin interface over the SSH service's RunCommand so that
// tests can inject a fake without needing a real SSH server.
type commandRunner interface {
	RunCommand(ctx context.Context, req sshclient.CommandRequest) (sshclient.CommandResult, error)
}

// bulkTarget pairs a resolved server with its row index in the job.
type bulkTarget struct {
	index  int
	server servers.Server
}

// ActionHandler handles POST /servers/bulk: it resolves the targets, creates
// a background job, and redirects to the live result page.
type ActionHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	runner     commandRunner
	jobs       *jobStore
}

func newActionHandler(deps module.Dependencies, serverRepo servers.Repository, runner commandRunner, jobs *jobStore) ActionHandler {
	return ActionHandler{deps: deps, serverRepo: serverRepo, runner: runner, jobs: jobs}
}

func (h ActionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid request", "The bulk action request could not be parsed.")
		return
	}

	action := strings.TrimSpace(r.FormValue("action"))
	rawIDs := r.Form["server_ids"]

	// Validate action.
	switch action {
	case "reboot", "update", "delete", "node-restart", "node-update":
	default:
		http.Redirect(w, r, "/servers?flash=bulk-invalid-action", http.StatusSeeOther)
		return
	}

	// Parse server IDs; skip non-numeric entries silently.
	ids := make([]int64, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id < 1 {
			continue
		}
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		http.Redirect(w, r, "/servers?flash=bulk-no-selection", http.StatusSeeOther)
		return
	}

	// Resolve every target up front (fast DB reads) so the result page shows
	// real names immediately, and rows that will never run (missing server,
	// no stored credentials) are final before any SSH work starts.
	rows := make([]view.BulkServerResultView, len(ids))
	targets := make([]bulkTarget, 0, len(ids))
	for i, id := range ids {
		server, err := h.serverRepo.GetByID(r.Context(), id)
		if err != nil {
			rows[i] = view.BulkServerResultView{
				ID:     id,
				Name:   fmt.Sprintf("#%d", id),
				Status: statusFailed,
				Reason: "server not found",
			}
			continue
		}

		rows[i] = view.BulkServerResultView{ID: id, Name: server.Name, Status: statusPending}

		if action != "delete" && !servers.HasStoredCredentials(server) {
			rows[i].Status = statusSkipped
			rows[i].Reason = "no stored credentials"
			continue
		}

		targets = append(targets, bulkTarget{index: i, server: server})
	}

	job := h.jobs.create(action, rows)
	go h.runJob(job, action, targets)

	http.Redirect(w, r, jobURL(job.id), http.StatusSeeOther)
}

// runJob executes the bulk action in the background, updating the job rows as
// each server completes.  It uses context.Background() deliberately: the work
// must survive the (already redirected) HTTP request.
func (h ActionHandler) runJob(job *job, action string, targets []bulkTarget) {
	defer job.finish()
	ctx := context.Background()

	if action == "delete" {
		for _, target := range targets {
			job.setStatus(target.index, statusRunning)
			job.setRow(target.index, h.deleteOne(ctx, target.server))
		}
		return
	}

	cmd, timeout := rebootCommand, bulkRebootTimeout
	switch action {
	case "update":
		cmd, timeout = updateCommand, bulkUpdateTimeout
	case "node-restart":
		cmd, timeout = nodeRestartCommand(), bulkNodeRestartTimeout
	case "node-update":
		cmd, timeout = nodeUpdateCommand(), bulkNodeUpdateTimeout
	}

	queue := make(chan bulkTarget, len(targets))
	for _, target := range targets {
		queue <- target
	}
	close(queue)

	var wg sync.WaitGroup
	for w := 0; w < bulkWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range queue {
				job.setStatus(target.index, statusRunning)
				job.setRow(target.index, h.execOne(ctx, target.server, cmd, timeout))
			}
		}()
	}
	wg.Wait()
}

// deleteOne removes a single server from the registry.
func (h ActionHandler) deleteOne(ctx context.Context, server servers.Server) view.BulkServerResultView {
	if err := h.serverRepo.Delete(ctx, server.ID); err != nil {
		return view.BulkServerResultView{
			ID:     server.ID,
			Name:   server.Name,
			Status: statusFailed,
			Reason: err.Error(),
		}
	}
	return view.BulkServerResultView{ID: server.ID, Name: server.Name, Status: statusOK}
}

// execOne runs cmd on a single (already credential-checked) server.
func (h ActionHandler) execOne(ctx context.Context, server servers.Server, cmd string, timeout time.Duration) view.BulkServerResultView {
	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)

	result, runErr := h.runner.RunCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: sshclient.ConnectionRequest{
			Host:           server.Host,
			Port:           server.Port,
			Username:       server.Username,
			AuthMode:       server.AuthMode,
			Password:       password,
			PrivateKeyPEM:  privateKey,
			KeyPassphrase:  keyPassphrase,
			ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
		},
		Command:        cmd,
		CommandTimeout: timeout,
	})

	if runErr != nil {
		return view.BulkServerResultView{
			ID:     server.ID,
			Name:   server.Name,
			Status: statusFailed,
			Reason: runErr.Error(),
		}
	}

	if result.ExitCode != nil && *result.ExitCode != 0 {
		return view.BulkServerResultView{
			ID:       server.ID,
			Name:     server.Name,
			Status:   statusFailed,
			ExitCode: strconv.Itoa(*result.ExitCode),
			Reason:   mapExitCode(*result.ExitCode, result.Stderr),
		}
	}

	exitStr := "0"
	if result.ExitCode != nil {
		exitStr = strconv.Itoa(*result.ExitCode)
	}
	return view.BulkServerResultView{
		ID:       server.ID,
		Name:     server.Name,
		Status:   statusOK,
		ExitCode: exitStr,
	}
}

// JobPageHandler renders GET /servers/bulk/jobs/{job}: the live (auto
// refreshing) or final result page for a background bulk job.
type JobPageHandler struct {
	deps module.Dependencies
	jobs *jobStore
}

func newJobPageHandler(deps module.Dependencies, jobs *jobStore) JobPageHandler {
	return JobPageHandler{deps: deps, jobs: jobs}
}

func (h JobPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	job, ok := h.jobs.get(strings.TrimSpace(r.PathValue("job")))
	if !ok {
		http.Redirect(w, r, "/servers?flash=bulk-job-expired", http.StatusSeeOther)
		return
	}

	rows, finished := job.snapshot()
	result := summarize(job.action, rows)
	result.Finished = finished
	if !finished {
		result.RefreshURL = jobURL(job.id)
	}

	page := view.NewPageData(h.deps.Config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Bulk action results"
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-bulk-result"
	page.PageTitle = "Bulk action results"
	actionLabel := humanizeBulkAction(job.action)
	if finished {
		page.PageDescription = "Per-server outcome for the bulk " + actionLabel + " operation."
	} else {
		page.PageDescription = "The bulk " + actionLabel + " operation is running — this page refreshes automatically."
	}
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.BulkActionResult = result
	page.PageStyles = []string{"/static/bulk.css"}

	if err := h.deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render bulk result page", http.StatusInternalServerError)
	}
}

func jobURL(id string) string {
	return "/servers/bulk/jobs/" + id
}

// humanizeBulkAction maps a raw action key to its human-facing label for the
// result page header and copy (e.g. "node-restart" → "node restart").
func humanizeBulkAction(action string) string {
	switch action {
	case "node-restart":
		return "node restart"
	case "node-update":
		return "node update"
	case "update":
		return "package update"
	default:
		return action
	}
}

// summarize tallies per-status counts for the result page header.
func summarize(action string, results []view.BulkServerResultView) view.BulkActionResultView {
	out := view.BulkActionResultView{
		Available:   true,
		Action:      action,
		ActionLabel: humanizeBulkAction(action),
		Results:     results,
		Total:       len(results),
	}
	for _, r := range results {
		switch r.Status {
		case statusOK:
			out.OKCount++
		case statusSkipped:
			out.SkippedCount++
		case statusPending, statusRunning:
			out.InProgressCount++
		default:
			out.FailedCount++
		}
	}
	return out
}

// mapExitCode converts well-known non-zero exit codes to human messages.
func mapExitCode(code int, stderr string) string {
	switch code {
	case exitSudoPassword:
		return "sudo requires password"
	case exitUnsupportedPkg:
		return "unsupported system"
	default:
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = fmt.Sprintf("exit %d", code)
		}
		return msg
	}
}
