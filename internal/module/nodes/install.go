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

// Installs run as background jobs: the official scripts pull Docker images or
// download binaries and can take many minutes, far beyond the HTTP write
// timeout. The POST creates a job and redirects to a live page that streams
// progress until done.
const (
	// installCommandTimeout backstops one streamed SSH session. The remote-side
	// `timeout` inside the provider command fires first in the normal case.
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

// Install progress stages. They form the ordered pipeline the UI renders as
// progress pills, and they are driven by real remote signals — providers embed
// stageMarker() at the exact point the phase begins, so a stage can never
// advance ahead of the work it names.
const (
	stageInitializing = "initializing"
	stageDownloading  = "downloading"
	stageDeploying    = "deploying"
	stageVerifying    = "verifying"
	stageReady        = "ready"
)

// InstallStageOrder is the canonical pipeline order used by the UI. Providers
// that skip a phase simply never emit its marker.
var InstallStageOrder = []string{
	stageInitializing,
	stageDownloading,
	stageDeploying,
	stageVerifying,
	stageReady,
}

var installStagePartial = "[NODEXIA"

// stageMarker is the remote-shell fragment a provider emits to advance the
// progress pipeline. It is a bare printf so it works in any POSIX shell, and
// the marker line is stripped from the user-visible log.
func stageMarker(stage string) string {
	return `printf "\n[NODEXIA:stage:` + stage + `]\n"; `
}

// stageKeyFor validates a stage key coming off the wire.
func stageKeyFor(key string) (string, bool) {
	for _, stage := range InstallStageOrder {
		if stage == key {
			return stage, true
		}
	}
	return "", false
}

// stageLabelKey returns the i18n key for a stage label.
func stageLabelKey(stage string) string { return "nodes.stage_" + stage }

// InstallStage is one streamed remote command in a provider's install plan.
// A provider's whole install is normally a single stage so it costs one SSH
// round trip; finer granularity comes from stageMarker() calls inside the
// command rather than from extra connections.
type InstallStage struct {
	// Key is the progress stage this command belongs to, used for the initial
	// pipeline position before any remote marker arrives.
	Key string
	// StartLog, when non-empty, is appended to the live output before the step
	// runs. The command string itself is never logged.
	StartLog string
	Command  string
	// Input is delivered over the SSH session rather than embedded in Command.
	// It may contain one-time credentials, so runInstall never appends it to the
	// job output. Empty means the command receives no managed stdin.
	Input string
	// AllocatePTY gives interactive upstream installers a controlling terminal.
	// Input is still written by Nodexia through that PTY, with terminal echo
	// disabled by sshclient, so /dev/tty reads work without exposing secrets.
	AllocatePTY bool
	// Timeout backstops this step's SSH session. 0 falls back to
	// installCommandTimeout.
	Timeout time.Duration
	// TolerateTimeout reports whether the remote `timeout` exit code (124)
	// counts as success — true for scripts that tail logs forever after a
	// successful install, false otherwise.
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
// sequence of streamed stages, then an optional registration readback. Modeling
// the install as data lets new providers and channels plug in without
// runInstall ever knowing the concrete provider type.
type InstallPlan struct {
	Stages   []InstallStage
	Readback InstallReadback
}

// installJob tracks one background node installation. A plan may carry secrets
// (the Rebecca certificate in InstallStage.Input), so the job is in-memory only
// and neither command strings nor managed input are written to job.output.
type installJob struct {
	id       string
	serverID int64
	nodeType string
	nodeName string
	plan     InstallPlan

	mu         sync.Mutex
	createdAt  time.Time
	finishedAt time.Time
	status     string
	output     string
	errMessage string
	info       *RegistrationInfo

	// stream is the line-oriented stage-marker extractor for this job.
	stream *streamProcessor
	// stage is the currently active progress stage and stageStartedAt when it
	// began; stageTimes records when each reached stage was entered so the UI
	// can show a per-stage duration.
	stage          string
	stageStartedAt time.Time
	stageTimes     map[string]time.Time
}

// appendOutput appends already-parsed text to the job log (used for the
// runner's own notices, which contain no stage markers).
func (j *installJob) appendOutput(chunk string) {
	chunk = ansi.Strip(chunk)
	if chunk == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.appendLocked(chunk)
}

// streamProcessor turns a chunked remote byte stream into a clean log while
// extracting stage markers. It is line-oriented because a marker is always
// emitted on its own line; a trailing partial line is withheld until it can be
// classified, so a marker split across two SSH chunks is still recognized and a
// partial marker is never printed.
//
// A marker is emitted as "\n[marker]\n", so it would otherwise double the line
// break that the preceding output already contributed. The processor therefore
// collapses that one redundant break (trimBlank) and swallows the single blank
// line it leaves behind, while leaving genuine blank lines in the script's
// output intact.
//
// It is deliberately a separate, dependency-free type so its behavior is
// directly unit-testable across every chunk boundary.
type streamProcessor struct {
	emit  func(string)
	stage func(string)
	// trimBlank drops one trailing newline from the accumulated log, used to
	// collapse the break a marker would otherwise duplicate.
	trimBlank func()
	// pending holds a trailing partial line that may still become a marker.
	pending string
	// suppressNextBlank swallows the blank line that immediately follows a
	// marker.
	suppressNextBlank bool
}

func (p *streamProcessor) feed(chunk string) {
	buf := p.pending + chunk
	p.pending = ""
	for {
		idx := strings.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}
		p.line(buf[:idx+1])
		buf = buf[idx+1:]
	}
	if buf == "" {
		return
	}
	// Withhold the tail only while it could still become a marker: an
	// unassembled marker line, a prefix of one, or a spinner line still
	// being rewritten with carriage returns.
	if strings.HasPrefix(buf, installStagePartial) || isPrefixOfMarker(buf) ||
		strings.ContainsRune(buf, '\r') || !strings.ContainsRune(buf, ']') {
		p.pending = buf
		return
	}
	p.line(buf)
}

func (p *streamProcessor) flush() {
	if p.pending == "" {
		return
	}
	pending := p.pending
	p.pending = ""
	p.line(pending)
}

func (p *streamProcessor) line(line string) {
	trimmed := strings.TrimRight(line, "\r")
	if strings.HasPrefix(trimmed, installStagePartial) && strings.HasSuffix(trimmed, "]") {
		stage := trimmed[len(installStagePartial)+1 : len(trimmed)-1]
		if key, ok := stageKeyFor(stage); ok {
			p.stage(key)
			p.suppressNextBlank = true
			p.trimBlank()
			return
		}
	}
	if p.suppressNextBlank {
		p.suppressNextBlank = false
		if line == "\n" || line == "\r\n" {
			return
		}
	}
	p.emit(line)
}

// isPrefixOfMarker reports whether buf is a proper prefix of a stage marker
// line, so it must not be emitted yet.
func isPrefixOfMarker(buf string) bool {
	return len(buf) < len(installStagePartial) && strings.HasPrefix(installStagePartial, buf)
}

// appendStream appends streamed remote output, extracting any stage markers.
// Markers are stripped from the visible log so the terminal stays clean.
func (j *installJob) appendStream(chunk string) {
	chunk = ansi.Strip(chunk)
	if chunk == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stream == nil {
		j.stream = &streamProcessor{
			emit:      j.appendLocked,
			stage:     j.advanceLocked,
			trimBlank: j.trimTrailingNewlineLocked,
		}
	}
	j.stream.feed(chunk)
}

// trimTrailingNewlineLocked drops exactly one trailing newline from the log so
// a marker does not duplicate the line break the previous output already made.
// Caller holds j.mu.
func (j *installJob) trimTrailingNewlineLocked() {
	j.output = strings.TrimSuffix(j.output, "\n")
}

// flushPending emits any buffered partial line. Called when a stage session
// ends so no output is lost.
func (j *installJob) flushPending() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stream != nil {
		j.stream.flush()
	}
}

// appendLocked writes text honoring the output cap. Caller holds j.mu.
func (j *installJob) appendLocked(chunk string) {
	if len(j.output) >= maxInstallOutputBytes {
		return
	}
	remaining := maxInstallOutputBytes - len(j.output)
	if len(chunk) > remaining {
		j.output += chunk[:remaining] + "\n[output truncated at 1 MB]"
		return
	}
	j.output += chunk
}

// advanceLocked records a stage transition. Caller holds j.mu.
func (j *installJob) advanceLocked(stage string) {
	if stage == j.stage {
		return
	}
	now := time.Now().UTC()
	if j.stageTimes == nil {
		j.stageTimes = map[string]time.Time{}
	}
	j.stage = stage
	j.stageStartedAt = now
	if _, seen := j.stageTimes[stage]; !seen {
		j.stageTimes[stage] = now
	}
}

// setStage advances the pipeline from the runner itself (verifying/ready).
func (j *installJob) setStage(stage string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.advanceLocked(stage)
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
	ID             string
	ServerID       int64
	NodeType       string
	NodeName       string
	Status         string
	Output         string
	Error          string
	Info           *RegistrationInfo
	CreatedAt      time.Time
	FinishedAt     time.Time
	Stage          string
	StageStartedAt time.Time
	StageTimes     map[string]time.Time
}

func (j *installJob) snapshot() installSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	output := j.output
	if j.stream != nil {
		output += j.stream.pending
	}
	snap := installSnapshot{
		ID:             j.id,
		ServerID:       j.serverID,
		NodeType:       j.nodeType,
		NodeName:       j.nodeName,
		Status:         j.status,
		Output:         output,
		Error:          j.errMessage,
		CreatedAt:      j.createdAt,
		FinishedAt:     j.finishedAt,
		Stage:          j.stage,
		StageStartedAt: j.stageStartedAt,
	}
	if len(j.stageTimes) > 0 {
		times := make(map[string]time.Time, len(j.stageTimes))
		for stage, at := range j.stageTimes {
			times[stage] = at
		}
		snap.StageTimes = times
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

func (s *installStore) create(serverID int64, nodeType, nodeName string, plan InstallPlan) *installJob {
	job := &installJob{
		id:        randomInstallJobID(),
		serverID:  serverID,
		nodeType:  nodeType,
		nodeName:  nodeName,
		plan:      plan,
		status:    installStatusRunning,
		createdAt: time.Now().UTC(),
	}
	job.stream = &streamProcessor{
		emit:      job.appendLocked,
		stage:     job.advanceLocked,
		trimBlank: job.trimTrailingNewlineLocked,
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
// remote stages followed by an optional configuration readback — and records
// the outcome on the job. It runs in a goroutine with context.Background():
// the work must survive the redirected HTTP request. The orchestration is fully
// provider-agnostic; everything provider-specific lives in the InstallPlan.
// onSuccess, when non-nil, runs once the install completes cleanly — used to
// auto-trigger node discovery so the freshly installed node appears without a
// manual refresh.
func runInstall(job *installJob, ssh *sshclient.Service, conn sshclient.ConnectionRequest, nodeHost string, onSuccess func()) {
	ctx := context.Background()

	job.setStage(stageInitializing)

	for _, step := range job.plan.Stages {
		if step.Key != "" {
			job.setStage(step.Key)
		}
		if step.StartLog != "" {
			job.appendOutput(step.StartLog)
		}
		timeout := step.Timeout
		if timeout <= 0 {
			timeout = installCommandTimeout
		}
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		commandReq := sshclient.CommandRequest{
			ConnectionRequest: conn,
			Command:           step.Command,
			CommandTimeout:    timeout,
			AllocatePTY:       step.AllocatePTY,
		}
		if step.Input != "" {
			commandReq.Stdin = strings.NewReader(step.Input)
		}
		result, runErr := ssh.StreamCommand(stepCtx, commandReq, sshclient.StreamHandlers{
			OnStdout: job.appendStream,
			OnStderr: job.appendStream,
		})
		cancel()
		job.flushPending()
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
		// below decides success. Stages that detach treat 124 as a real failure.
		if exitCode == exitRemoteTimeout && step.TolerateTimeout {
			job.appendOutput("\n[log streaming stopped — verifying the installation]\n")
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
		job.setStage(stageReady)
		job.complete(nil)
		if onSuccess != nil {
			onSuccess()
		}
		return
	}

	job.setStage(stageVerifying)
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
	job.setStage(stageReady)
	job.complete(&info)
	if onSuccess != nil {
		onSuccess()
	}
}

func installFailureMessage(exitCode int, stderr string) string {
	switch exitCode {
	case exitSudoPassword:
		return "The install needs root or passwordless sudo on the target server."
	case exitPromptContractChange:
		return "The upstream installer no longer matches the expected structure; nothing was changed on the server. Update Nodexia before retrying."
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
