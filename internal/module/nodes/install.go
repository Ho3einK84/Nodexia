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

// installJob tracks one background PasarGuard node installation.
type installJob struct {
	id       string
	serverID int64
	nodeName string
	config   InstallConfig

	mu         sync.Mutex
	createdAt  time.Time
	finishedAt time.Time
	status     string
	output     string
	errMessage string
	info       *RegistrationInfo
}

func (j *installJob) appendOutput(chunk string) {
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

func (j *installJob) complete(info RegistrationInfo) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = installStatusCompleted
	j.info = &info
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

func (s *installStore) create(serverID int64, nodeName string, config InstallConfig) *installJob {
	job := &installJob{
		id:        randomInstallJobID(),
		serverID:  serverID,
		nodeName:  nodeName,
		config:    config,
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

// runInstall executes the official install script and then verifies the
// result by reading the installed configuration.  It runs in a goroutine with
// context.Background(): the work must survive the redirected HTTP request.
func runInstall(job *installJob, ssh *sshclient.Service, conn sshclient.ConnectionRequest, provider PasarGuardProvider, nodeHost string) {
	ctx := context.Background()

	installCmd, err := provider.InstallCommand(job.nodeName)
	if err != nil {
		job.fail(err.Error())
		return
	}

	result, runErr := ssh.StreamCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: conn,
		Command:           installCmd,
		CommandTimeout:    installCommandTimeout,
	}, sshclient.StreamHandlers{
		OnStdout: job.appendOutput,
		OnStderr: job.appendOutput,
	})

	if runErr != nil {
		job.appendOutput("\n[install error] " + runErr.Error() + "\n")
		job.fail("Install command failed: " + runErr.Error())
		return
	}

	exitCode := -1
	if result.ExitCode != nil {
		exitCode = *result.ExitCode
	}
	// The script tails container logs after a successful install, so the
	// remote `timeout` cutting it off (124) is expected — the verification
	// probe below decides whether the install actually succeeded.
	if exitCode != 0 && exitCode != exitRemoteTimeout {
		job.fail(installFailureMessage(exitCode, result.Stderr))
		return
	}
	if exitCode == exitRemoteTimeout {
		job.appendOutput("\n[log streaming stopped — applying configuration]\n")
	}

	// The official installer only writes defaults (port 62050, gRPC,
	// auto-generated key). Apply the panel's chosen ports/protocol/key by
	// patching /opt/<name>/.env and restarting through the official CLI.
	configureCmd, timeout, err := provider.ConfigureCommand(job.nodeName, job.config)
	if err != nil {
		job.fail(err.Error())
		return
	}
	job.appendOutput(fmt.Sprintf("\n[configuring node: service port %s, protocol %s]\n", job.config.ServicePort, job.config.Protocol))
	configCtx, cancelConfig := context.WithTimeout(ctx, timeout)
	configResult, configErr := ssh.StreamCommand(configCtx, sshclient.CommandRequest{
		ConnectionRequest: conn,
		Command:           configureCmd,
		CommandTimeout:    timeout,
	}, sshclient.StreamHandlers{
		OnStdout: job.appendOutput,
		OnStderr: job.appendOutput,
	})
	cancelConfig()
	if configErr != nil {
		job.fail("Node installed but applying the configuration failed: " + configErr.Error())
		return
	}
	if configResult.ExitCode != nil && *configResult.ExitCode != 0 {
		job.fail(fmt.Sprintf("Node installed but configuration exited with code %d. Check the output above.", *configResult.ExitCode))
		return
	}

	infoCmd, err := provider.RegistrationInfoCommand(job.nodeName)
	if err != nil {
		job.fail(err.Error())
		return
	}

	infoCtx, cancel := context.WithTimeout(ctx, installInfoTimeout)
	defer cancel()
	infoResult, infoErr := ssh.RunCommand(infoCtx, sshclient.CommandRequest{
		ConnectionRequest: conn,
		Command:           infoCmd,
		CommandTimeout:    installInfoTimeout,
	})
	if infoErr != nil {
		job.fail("Install finished but reading the node configuration failed: " + infoErr.Error())
		return
	}

	info, found := ParseRegistrationInfo(job.nodeName, infoResult.Stdout)
	if !found {
		job.fail(fmt.Sprintf("Install did not produce /opt/%s/.env — inspect the output above for the script error.", job.nodeName))
		return
	}
	info.NodeIP = strings.TrimSpace(nodeHost)
	job.complete(info)
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
