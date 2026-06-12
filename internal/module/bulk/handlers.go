package bulk

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

const bulkWorkers = 5

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

// commandRunner is a thin interface over the SSH service's RunCommand so that
// tests can inject a fake without needing a real SSH server.
type commandRunner interface {
	RunCommand(ctx context.Context, req sshclient.CommandRequest) (sshclient.CommandResult, error)
}

// ActionHandler handles POST /servers/bulk.
type ActionHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	runner     commandRunner
}

func NewActionHandler(deps module.Dependencies, serverRepo servers.Repository) ActionHandler {
	return ActionHandler{deps: deps, serverRepo: serverRepo, runner: deps.SSH}
}

// newActionHandlerWithRunner is used by tests to inject a fake runner.
func newActionHandlerWithRunner(deps module.Dependencies, serverRepo servers.Repository, runner commandRunner) ActionHandler {
	return ActionHandler{deps: deps, serverRepo: serverRepo, runner: runner}
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
	case "reboot", "update", "delete":
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

	var results []view.BulkServerResultView

	switch action {
	case "delete":
		results = h.runDelete(r.Context(), ids)
	case "reboot":
		results = h.runSSHBulk(r.Context(), ids, rebootCommand, action)
	case "update":
		results = h.runSSHBulk(r.Context(), ids, updateCommand, action)
	}

	page := view.NewPageData(h.deps.Config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Bulk action results"
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-bulk-result"
	page.PageTitle = "Bulk action results"
	page.PageDescription = "Per-server outcome for the bulk " + action + " operation."
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.BulkActionResult = summarize(action, results)

	if err := h.deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render bulk result page", http.StatusInternalServerError)
	}
}

// runDelete loops over IDs and deletes each from the database sequentially.
func (h ActionHandler) runDelete(ctx context.Context, ids []int64) []view.BulkServerResultView {
	results := make([]view.BulkServerResultView, 0, len(ids))
	for _, id := range ids {
		server, err := h.serverRepo.GetByID(ctx, id)
		name := fmt.Sprintf("#%d", id)
		if err == nil {
			name = server.Name
		}
		if err := h.serverRepo.Delete(ctx, id); err != nil {
			results = append(results, view.BulkServerResultView{
				ID:     id,
				Name:   name,
				Status: "failed",
				Reason: err.Error(),
			})
		} else {
			results = append(results, view.BulkServerResultView{
				ID:     id,
				Name:   name,
				Status: "ok",
			})
		}
	}
	return results
}

// workerJob carries per-server input for the bounded SSH worker pool.
type workerJob struct {
	index int
	id    int64
}

// runSSHBulk runs cmd against each server in ids using a bounded pool of
// bulkWorkers goroutines.  Servers without stored credentials are skipped.
func (h ActionHandler) runSSHBulk(ctx context.Context, ids []int64, cmd, action string) []view.BulkServerResultView {
	results := make([]view.BulkServerResultView, len(ids))

	jobs := make(chan workerJob, len(ids))
	for i, id := range ids {
		jobs <- workerJob{index: i, id: id}
	}
	close(jobs)

	var wg sync.WaitGroup
	for w := 0; w < bulkWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results[job.index] = h.runOneServer(ctx, job.id, cmd)
			}
		}()
	}
	wg.Wait()
	return results
}

// runOneServer executes cmd on a single server and returns the result view.
func (h ActionHandler) runOneServer(ctx context.Context, id int64, cmd string) view.BulkServerResultView {
	server, err := h.serverRepo.GetByID(ctx, id)
	if err != nil {
		return view.BulkServerResultView{
			ID:     id,
			Name:   fmt.Sprintf("#%d", id),
			Status: "failed",
			Reason: "server not found",
		}
	}

	if !servers.HasStoredCredentials(server) {
		return view.BulkServerResultView{
			ID:     id,
			Name:   server.Name,
			Status: "skipped",
			Reason: "no stored credentials",
		}
	}

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
		CommandTimeout: h.deps.Config.SSH.CommandTimeout,
	})

	if runErr != nil {
		return view.BulkServerResultView{
			ID:     id,
			Name:   server.Name,
			Status: "failed",
			Reason: runErr.Error(),
		}
	}

	if result.ExitCode != nil && *result.ExitCode != 0 {
		reason := mapExitCode(*result.ExitCode, result.Stderr)
		return view.BulkServerResultView{
			ID:       id,
			Name:     server.Name,
			Status:   "failed",
			ExitCode: strconv.Itoa(*result.ExitCode),
			Reason:   reason,
		}
	}

	exitStr := "0"
	if result.ExitCode != nil {
		exitStr = strconv.Itoa(*result.ExitCode)
	}
	return view.BulkServerResultView{
		ID:       id,
		Name:     server.Name,
		Status:   "ok",
		ExitCode: exitStr,
	}
}

// summarize tallies per-status counts for the result page header.
func summarize(action string, results []view.BulkServerResultView) view.BulkActionResultView {
	out := view.BulkActionResultView{
		Available: true,
		Action:    action,
		Results:   results,
		Total:     len(results),
	}
	for _, r := range results {
		switch r.Status {
		case "ok":
			out.OKCount++
		case "skipped":
			out.SkippedCount++
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
