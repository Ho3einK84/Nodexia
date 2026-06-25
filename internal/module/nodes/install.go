package nodes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/ansi"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

// PasarGuard installs run as background jobs: the official script pulls
// Docker images and can take many minutes, far beyond the HTTP write timeout.
// The POST creates a job and redirects to a live page that polls until done.
const (
	// installCommandTimeout backstops the whole SSH session.  The remote-side
	// `timeout` inside InstallCommand fires first in the normal case.
	installCommandTimeout = 15 * time.Minute
	// installInfoTimeout bounds the post-install verification probe.
	installInfoTimeout = 2 * time.Minute

	installStatusRunning   = "running"
	installStatusCompleted = "completed"
	installStatusFailed    = "failed"

	// Mirror the bulk job store retention behavior.
	installFinishedTTL = 30 * time.Minute
	installStaleTTL    = 2 * time.Hour

	maxInstallOutputBytes = 1 << 20 // 1 MiB of streamed script output
)

// InstallStep is one streamed remote command in a provider's install plan.
type InstallStep struct {
	// StartLog, when non-empty, is appended to the live output before the step
	// runs. The command string itself is NEVER logged: it may carry secrets
	// (e.g. the Rebecca certificate is base64-embedded in the install command).
	StartLog string
	Command  string
	// Timeout backstops this step's SSH session. 0 falls back to
	// installCommandTimeout.
	Timeout time.Duration
	// TolerateTimeout reports whether the remote `timeout` exit code (124)
	// counts as success — true for scripts that tail logs forever after a
	// successful install (PasarGuard), false otherwise (Rebecca detaches).
	TolerateTimeout bool
}

// InstallReadback optionally re-reads the installed configuration so the job
// page can show registration details. Providers whose install model has the
// user supply credentials up front (Rebecca: the cert comes FROM the panel)
// leave it zero — there is nothing to read back.
type InstallReadback struct {
	Command string
	// Parse turns the readback stdout into registration info. found=false means
	// the install did not actually produce the expected configuration.
	Parse func(stdout string) (RegistrationInfo, bool)
	// NotFoundMessage is the failure shown when Parse reports found=false.
	NotFoundMessage string
}

// InstallPlan is the provider-agnostic recipe runInstall executes: an ordered
// sequence of streamed steps, then an optional registration readback. Modeling
// the install as data lets new providers (Rebecca) and channels (dev/stable)
// plug in without runInstall ever knowing the concrete provider type — the
// PasarGuard read-back model and the Rebecca user-supplied-cert model are both
// just different plans.
type InstallPlan struct {
	Steps    []InstallStep
	Readback InstallReadback
}

// installJob tracks one background node installation. The plan it carries may
// embed secrets (the Rebecca certificate) in its command strings, so the job
// is in-memory only and the command strings are never written to job.output.
type installJob struct {
	id       string
	serverID int64
	nodeName string
	plan     InstallPlan

	mu         sync.Mutex
	createdAt  time.Time
	finishedAt time.Time
	status     string
	output     string
	errMessage string
	info       *RegistrationInfo
}

func (j *installJob) appendOutput(chunk string) {
	chunk = ansi.Strip(chunk)
	if chunk == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.output) >= maxInstallOutputBytes {
		return
	}
	remaining := maxInstallOutputBytes - len(j.output)
	if len(chunk) > remaining {
		j.output += chunk[:remaining] + "\n[output truncated at 1 MiB]"
		return
	}
	j.output += chunk
}

func (j *installJob) complete(info *RegistrationInfo) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = installStatusCompleted
	j.info = info
	j.finishedAt = time.Now().UTC()
}

func (j *installJob) fail(message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = installStatusFailed
	j.errMessage = message
	j.finishedAt = time.Now().UTC()
}

// installSnapshot is a render-safe copy of the mutable job state.
type installSnapshot struct {
	ID         string
	ServerID   int64
	NodeName   string
	Status     string
	Output     string
	Error      string
	Info       *RegistrationInfo
	CreatedAt  time.Time
	FinishedAt time.Time
}

func (j *installJob) snapshot() installSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	snap := installSnapshot{
		ID:         j.id,
		ServerID:   j.serverID,
		NodeName:   j.nodeName,
		Status:     j.status,
		Output:     j.output,
		Error:      j.errMessage,
		CreatedAt:  j.createdAt,
		FinishedAt: j.finishedAt,
	}
	if j.info != nil {
		info := *j.info
		snap.Info = &info
	}
	return snap
}

// installStore keeps in-flight and recently finished install jobs in memory.
// Jobs are not persisted — the API key and certificate they carry must never
// touch the database.  After a restart the job page shows an "expired" flash.
type installStore struct {
	mu   sync.Mutex
	jobs map[string]*installJob
}

func newInstallStore() *installStore {
	return &installStore{jobs: map[string]*installJob{}}
}

func (s *installStore) create(serverID int64, nodeName string, plan InstallPlan) *installJob {
	job := &installJob{
		id:        randomInstallJobID(),
		serverID:  serverID,
		nodeName:  nodeName,
		plan:      plan,
		status:    installStatusRunning,
		createdAt: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.jobs[job.id] = job
	return job
}

func (s *installStore) get(id string) (*installJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *installStore) pruneLocked() {
	now := time.Now().UTC()
	for id, job := range s.jobs {
		job.mu.Lock()
		expired := (!job.finishedAt.IsZero() && now.Sub(job.finishedAt) > installFinishedTTL) ||
			now.Sub(job.createdAt) > installStaleTTL
		job.mu.Unlock()
		if expired {
			delete(s.jobs, id)
		}
	}
}

func randomInstallJobID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Practically unreachable; the id is only a lookup key.
		return "install-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// runInstall executes the provider's install plan — a sequence of streamed
// remote commands followed by an optional configuration readback — and records
// the outcome on the job. It runs in a goroutine with context.Background():
// the work must survive the redirected HTTP request. The orchestration is fully
// provider-agnostic; everything provider-specific lives in the InstallPlan.
// runInstall executes the install plan in the background. onSuccess, when
// non-nil, runs once the install completes cleanly — used to auto-trigger node
// discovery so the freshly installed node appears without a manual refresh.
func runInstall(job *installJob, ssh *sshclient.Service, conn sshclient.ConnectionRequest, nodeHost string, onSuccess func()) {
	ctx := context.Background()

	for _, step := range job.plan.Steps {
		if step.StartLog != "" {
			job.appendOutput(step.StartLog)
		}
		timeout := step.Timeout
		if timeout <= 0 {
			timeout = installCommandTimeout
		}
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		result, runErr := ssh.StreamCommand(stepCtx, sshclient.CommandRequest{
			ConnectionRequest: conn,
			Command:           step.Command,
			CommandTimeout:    timeout,
		}, sshclient.StreamHandlers{
			OnStdout: job.appendOutput,
			OnStderr: job.appendOutput,
		})
		cancel()
		if runErr != nil {
			job.appendOutput("\n[install error] " + runErr.Error() + "\n")
			job.fail("Install command failed: " + runErr.Error())
			return
		}

		exitCode := -1
		if result.ExitCode != nil {
			exitCode = *result.ExitCode
		}
		// Some scripts tail container logs after a successful install, so the
		// remote `timeout` cutting them off (124) is expected and the readback
		// below decides success. Steps that detach (Rebecca) treat 124 as a
		// real failure.
		if exitCode == exitRemoteTimeout && step.TolerateTimeout {
			job.appendOutput("\n[log streaming stopped — applying configuration]\n")
			continue
		}
		if exitCode != 0 {
			job.fail(installFailureMessage(exitCode, result.Stderr))
			return
		}
	}

	// No readback configured: the provider supplied everything needed up front
	// (Rebecca's user-provided certificate), so a clean exit is success.
	if job.plan.Readback.Command == "" {
		job.complete(nil)
		if onSuccess != nil {
			onSuccess()
		}
		return
	}

	infoCtx, cancel := context.WithTimeout(ctx, installInfoTimeout)
	defer cancel()
	infoResult, infoErr := ssh.RunCommand(infoCtx, sshclient.CommandRequest{
		ConnectionRequest: conn,
		Command:           job.plan.Readback.Command,
		CommandTimeout:    installInfoTimeout,
	})
	if infoErr != nil {
		job.fail("Install finished but reading the node configuration failed: " + infoErr.Error())
		return
	}

	info, found := job.plan.Readback.Parse(infoResult.Stdout)
	if !found {
		job.fail(job.plan.Readback.NotFoundMessage)
		return
	}
	info.NodeIP = strings.TrimSpace(nodeHost)
	job.complete(&info)
	if onSuccess != nil {
		onSuccess()
	}
}

func installFailureMessage(exitCode int, stderr string) string {
	switch exitCode {
	case exitSudoPassword:
		return "The install needs root or passwordless sudo on the target server."
	case exitNoDownloader:
		return "The target server has neither curl nor wget to download the install script."
	default:
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = fmt.Sprintf("install script exited with code %d", exitCode)
		}
		return "Install failed: " + message
	}
}
