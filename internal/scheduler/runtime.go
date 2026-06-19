package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/notify"
	"github.com/Ho3einK84/Nodexia/internal/notify/telegram"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

type JobType string

const (
	JobMonitoring JobType = "monitoring"
	JobNodes      JobType = "nodes"
)

type Overview struct {
	Enabled            bool
	StartupDelay       time.Duration
	SweepInterval      time.Duration
	MonitoringInterval time.Duration
	NodesInterval      time.Duration
	RetryBackoff       time.Duration
	EligibleJobs       int
	BlockedJobs        int
	RunningJobs        int
	Jobs               []JobSnapshot
}

type JobSnapshot struct {
	ServerID            int64
	ServerName          string
	JobType             JobType
	Status              string
	Reason              string
	LastMessage         string
	LastError           string
	LastStartedAt       time.Time
	LastCompletedAt     time.Time
	LastSuccessAt       time.Time
	NextRunAt           time.Time
	LastDuration        time.Duration
	ConsecutiveFailures int
	Paused              bool
}

type Runtime struct {
	cfg           config.SchedulerConfig
	ssh           *sshclient.Service
	serverRepo    servers.Repository
	monitorRepo   monitoring.Repository
	trafficRepo   monitoring.TrafficRepository
	nodeRepo      nodes.Repository
	providers     []nodes.Provider
	evaluator     *alerts.Evaluator
	analyticsRepo analytics.Repository
	forecastSvc   *analytics.ForecastService
	rollupSvc     *analytics.RollupService
	cleanupSvc    *analytics.CleanupService

	mu     sync.RWMutex
	jobs   map[string]*jobState
	paused map[string]struct{} // keys from jobKey(); protected by mu
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type jobState struct {
	ServerID            int64
	ServerName          string
	JobType             JobType
	Allowed             bool
	Reason              string
	Running             bool
	LastMessage         string
	LastError           string
	LastStartedAt       time.Time
	LastCompletedAt     time.Time
	LastSuccessAt       time.Time
	NextRunAt           time.Time
	LastDuration        time.Duration
	ConsecutiveFailures int
}

type eligibility struct {
	Allowed bool
	Reason  string
}

type resolvedCredential struct {
	Password         string
	PrivateKeyPEM    string
	KeyPassphrase    string
	TrafficInterface string
}

func New(cfg config.Config, conn *sql.DB, ssh *sshclient.Service) *Runtime {
	if conn == nil || ssh == nil {
		return nil
	}

	analyticsRepo := analytics.NewSQLRepository(conn)
	runtime := &Runtime{
		cfg:           cfg.Scheduler,
		ssh:           ssh,
		serverRepo:    servers.NewSQLRepository(conn),
		monitorRepo:   monitoring.NewSQLRepository(conn),
		trafficRepo:   monitoring.NewSQLRepository(conn),
		nodeRepo:      nodes.NewSQLRepository(conn),
		providers:     nodes.DefaultProviders(),
		evaluator:     alerts.NewEvaluator(alerts.NewSQLRepository(conn), schedulerNotifier(cfg)),
		analyticsRepo: analyticsRepo,
		forecastSvc:   analytics.NewForecastService(),
		rollupSvc:     analytics.NewRollupService(analyticsRepo),
		cleanupSvc:    analytics.NewCleanupService(analyticsRepo),
		jobs:          map[string]*jobState{},
		paused:        map[string]struct{}{},
	}
	runtime.refresh(context.Background(), false)
	return runtime
}

// PauseJob prevents the job for (serverID, jobType) from launching until
// ResumeJob is called. A currently running job is not interrupted.
func (r *Runtime) PauseJob(serverID int64, jobType JobType) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.paused[jobKey(serverID, jobType)] = struct{}{}
	r.mu.Unlock()
}

// ResumeJob re-enables a previously paused job. If the job was due to run
// while paused, it will run on the next scheduler sweep.
func (r *Runtime) ResumeJob(serverID int64, jobType JobType) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.paused, jobKey(serverID, jobType))
	r.mu.Unlock()
}

// ToggleJob pauses the job if it is currently unpaused, or resumes it if it is
// currently paused. Returns true when the job is now paused.
func (r *Runtime) ToggleJob(serverID int64, jobType JobType) bool {
	if r == nil {
		return false
	}
	key := jobKey(serverID, jobType)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, paused := r.paused[key]; paused {
		delete(r.paused, key)
		return false
	}
	r.paused[key] = struct{}{}
	return true
}

// IsJobPaused reports whether the job for (serverID, jobType) is currently paused.
func (r *Runtime) IsJobPaused(serverID int64, jobType JobType) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	_, paused := r.paused[jobKey(serverID, jobType)]
	r.mu.RUnlock()
	return paused
}

// schedulerNotifier builds a Telegram notifier when a bot token is configured,
// or nil when it is not. A nil notifier keeps evaluation recording events while
// skipping message delivery. A typed-nil is never returned as a non-nil
// interface, so this only constructs the client when the token is present.
func schedulerNotifier(cfg config.Config) notify.Notifier {
	token := strings.TrimSpace(cfg.Notify.TelegramBotToken)
	if token == "" {
		return nil
	}
	return telegram.NewClient(token)
}

func (r *Runtime) Start() {
	if r == nil || !r.cfg.Enabled || r.cancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(3)
	go r.loop(ctx)
	go r.analyticsLoop(ctx)
	go r.countryLoop(ctx)
}

// analyticsLoop runs hourly metric rollups and daily cleanup on independent
// tickers so they do not interfere with the per-server monitoring sweep.
func (r *Runtime) analyticsLoop(ctx context.Context) {
	defer r.wg.Done()
	if r.rollupSvc == nil {
		return
	}

	hourlyTicker := time.NewTicker(time.Hour)
	defer hourlyTicker.Stop()
	dailyTicker := time.NewTicker(24 * time.Hour)
	defer dailyTicker.Stop()

	// Run an initial rollup shortly after startup so historical data is
	// available without waiting a full hour.
	startTimer := time.NewTimer(5 * time.Minute)
	defer startTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-startTimer.C:
			r.rollupSvc.ComputeHourlyRollups(ctx)
			r.rollupSvc.ComputeDailyRollups(ctx)
		case <-hourlyTicker.C:
			r.rollupSvc.ComputeHourlyRollups(ctx)
			r.rollupSvc.ComputeDailyRollups(ctx)
		case <-dailyTicker.C:
			if r.cleanupSvc != nil {
				r.cleanupSvc.RunCleanup(ctx)
			}
		}
	}
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

func (r *Runtime) Overview(limit int) Overview {
	if r == nil {
		return Overview{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	jobs := make([]JobSnapshot, 0, len(r.jobs))
	overview := Overview{
		Enabled:            r.cfg.Enabled,
		StartupDelay:       r.cfg.StartupDelay,
		SweepInterval:      r.cfg.SweepInterval,
		MonitoringInterval: r.cfg.MonitoringInterval,
		NodesInterval:      r.cfg.NodesInterval,
		RetryBackoff:       r.cfg.RetryBackoff,
	}

	for _, state := range r.jobs {
		if state.Allowed {
			overview.EligibleJobs++
		} else {
			overview.BlockedJobs++
		}
		if state.Running {
			overview.RunningJobs++
		}

		key := jobKey(state.ServerID, state.JobType)
		_, isPaused := r.paused[key]
		jobs = append(jobs, JobSnapshot{
			ServerID:            state.ServerID,
			ServerName:          state.ServerName,
			JobType:             state.JobType,
			Status:              state.status(),
			Reason:              state.Reason,
			LastMessage:         state.LastMessage,
			LastError:           state.LastError,
			LastStartedAt:       state.LastStartedAt,
			LastCompletedAt:     state.LastCompletedAt,
			LastSuccessAt:       state.LastSuccessAt,
			NextRunAt:           state.NextRunAt,
			LastDuration:        state.LastDuration,
			ConsecutiveFailures: state.ConsecutiveFailures,
			Paused:              isPaused,
		})
	}

	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].LastStartedAt.Equal(jobs[j].LastStartedAt) {
			return jobs[i].LastStartedAt.After(jobs[j].LastStartedAt)
		}
		if !jobs[i].NextRunAt.Equal(jobs[j].NextRunAt) {
			if jobs[i].NextRunAt.IsZero() {
				return false
			}
			if jobs[j].NextRunAt.IsZero() {
				return true
			}
			return jobs[i].NextRunAt.Before(jobs[j].NextRunAt)
		}
		if jobs[i].ServerName != jobs[j].ServerName {
			return jobs[i].ServerName < jobs[j].ServerName
		}
		return jobs[i].JobType < jobs[j].JobType
	})

	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	overview.Jobs = jobs
	return overview
}

func (r *Runtime) loop(ctx context.Context) {
	defer r.wg.Done()

	if r.cfg.StartupDelay > 0 {
		timer := time.NewTimer(r.cfg.StartupDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}

	r.refresh(ctx, true)

	ticker := time.NewTicker(r.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refresh(ctx, true)
		}
	}
}

func (r *Runtime) refresh(ctx context.Context, launchJobs bool) {
	serversList, err := r.serverRepo.List(ctx)
	if err != nil {
		return
	}

	now := time.Now().UTC()
	seen := make(map[string]struct{}, len(serversList)*2)
	for _, server := range serversList {
		for _, jobType := range []JobType{JobMonitoring, JobNodes} {
			key := jobKey(server.ID, jobType)
			seen[key] = struct{}{}

			state := r.ensureState(server, jobType)
			eligible := r.evaluateEligibility(server)

			r.mu.Lock()
			state.ServerName = server.Name
			state.Reason = eligible.Reason
			state.Allowed = eligible.Allowed
			if !state.Allowed {
				state.NextRunAt = time.Time{}
			} else if state.NextRunAt.IsZero() {
				state.NextRunAt = now
			}
			_, isPaused := r.paused[key]
			shouldLaunch := launchJobs && state.Allowed && !state.Running && !state.NextRunAt.After(now) && !isPaused
			r.mu.Unlock()

			if shouldLaunch {
				r.launchJob(ctx, server, jobType)
			}
		}
	}

	r.mu.Lock()
	for key, state := range r.jobs {
		if _, ok := seen[key]; ok || state.Running {
			continue
		}
		delete(r.jobs, key)
	}
	r.mu.Unlock()
}

func (r *Runtime) evaluateEligibility(server servers.Server) eligibility {
	switch server.CredentialStrategy {
	case servers.CredentialStrategyStored:
		// The SSH password is stored directly in credential_ref.
		if strings.TrimSpace(server.CredentialRef) == "" {
			return eligibility{Reason: "No password stored for this server. Edit the server and enter a password."}
		}
		return eligibility{Allowed: true, Reason: "Stored password is available for background connections."}

	case servers.CredentialStrategyRuntime:
		// Historically "runtime" meant per-request only, but the server form has
		// always persisted the password into credential_ref when provided.  If a
		// password is present we allow scheduled runs for backward compatibility.
		if strings.TrimSpace(server.CredentialRef) == "" {
			return eligibility{Reason: "No password stored. Edit the server and enter a password, or switch to the stored strategy to enable scheduler jobs."}
		}
		return eligibility{Allowed: true, Reason: "Stored password available (runtime strategy with saved credentials)."}

	case servers.CredentialStrategyAgentReady:
		// The SSH agent (SSH_AUTH_SOCK) provides keys to the scheduler process.
		if os.Getenv("SSH_AUTH_SOCK") == "" {
			return eligibility{Reason: "SSH_AUTH_SOCK is not set in the server process environment; start Nodexia with a running SSH agent to enable agent_ready jobs."}
		}
		return eligibility{Allowed: true, Reason: "SSH agent socket is available for background connections."}

	case servers.CredentialStrategyExternalRef:
		if strings.TrimSpace(server.CredentialRef) == "" {
			return eligibility{Reason: "External credential reference is empty."}
		}
		if _, err := parseCredentialReference(server.CredentialRef); err != nil {
			return eligibility{Reason: err.Error()}
		}
		return eligibility{Allowed: true, Reason: "External credential reference can be resolved by the scheduler."}

	default:
		return eligibility{Reason: "Unsupported credential strategy."}
	}
}

func (r *Runtime) launchJob(parent context.Context, server servers.Server, jobType JobType) {
	state := r.ensureState(server, jobType)

	r.mu.Lock()
	if state.Running || !state.Allowed {
		r.mu.Unlock()
		return
	}
	state.Running = true
	state.LastStartedAt = time.Now().UTC()
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		timeout := r.cfg.ConnectTimeout + r.cfg.CommandTimeout + (5 * time.Second)
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()

		startedAt := time.Now().UTC()
		message, err := r.executeJob(ctx, server, jobType)
		finishedAt := time.Now().UTC()

		r.mu.Lock()
		defer r.mu.Unlock()

		state := r.jobs[jobKey(server.ID, jobType)]
		if state == nil {
			return
		}

		state.Running = false
		state.LastCompletedAt = finishedAt
		state.LastDuration = finishedAt.Sub(startedAt)
		state.LastMessage = strings.TrimSpace(message)

		if err != nil {
			state.LastError = err.Error()
			state.ConsecutiveFailures++
			state.NextRunAt = finishedAt.Add(r.cfg.RetryBackoff)
			return
		}

		state.LastError = ""
		state.LastSuccessAt = finishedAt
		state.ConsecutiveFailures = 0
		state.NextRunAt = finishedAt.Add(r.intervalFor(jobType))
	}()
}

// connectionRequestFor resolves a server's SSH credentials (honouring its
// credential strategy) and builds a CommandRequest ready for a command to be
// attached. It is shared by the monitoring/nodes jobs and the country resolver
// so credential handling stays in exactly one place. It also returns the
// resolved credential so callers that need its extra fields (e.g. the traffic
// interface) can use them.
func (r *Runtime) connectionRequestFor(server servers.Server) (sshclient.CommandRequest, resolvedCredential, error) {
	var credentials resolvedCredential
	authMode := server.AuthMode

	switch server.CredentialStrategy {
	case servers.CredentialStrategyAgentReady:
		// Leave credentials empty and clear AuthMode so sshclient falls through
		// to its SSH-agent branch (the default case in authMethods).
		authMode = ""

	case servers.CredentialStrategyStored, servers.CredentialStrategyRuntime:
		// Both strategies store the literal SSH password in credential_ref.
		// They cannot go through resolveCredential() which expects key=value format.
		credentials = resolvedCredential{
			Password: strings.TrimSpace(server.CredentialRef),
		}

	default:
		// external_ref and anything else: resolve via environment / file references.
		var err error
		credentials, err = resolveCredential(server.AuthMode, server.CredentialRef)
		if err != nil {
			return sshclient.CommandRequest{}, resolvedCredential{}, err
		}
	}

	req := sshclient.CommandRequest{
		ConnectionRequest: sshclient.ConnectionRequest{
			Host:           server.Host,
			Port:           server.Port,
			Username:       server.Username,
			AuthMode:       authMode,
			Password:       credentials.Password,
			PrivateKeyPEM:  credentials.PrivateKeyPEM,
			KeyPassphrase:  credentials.KeyPassphrase,
			ConnectTimeout: r.cfg.ConnectTimeout,
		},
		CommandTimeout: r.cfg.CommandTimeout,
	}
	return req, credentials, nil
}

func (r *Runtime) executeJob(ctx context.Context, server servers.Server, jobType JobType) (string, error) {
	req, credentials, err := r.connectionRequestFor(server)
	if err != nil {
		return "", err
	}

	switch jobType {
	case JobMonitoring:
		return r.executeMonitoringJob(ctx, server, req, credentials)
	case JobNodes:
		return r.executeNodesJob(ctx, server, req)
	default:
		return "", fmt.Errorf("scheduler: unsupported job type %q", jobType)
	}
}

func (r *Runtime) executeMonitoringJob(ctx context.Context, server servers.Server, req sshclient.CommandRequest, credentials resolvedCredential) (string, error) {
	snapshot, _, err := monitoring.Collect(ctx, r.ssh, req)
	if err != nil {
		return "", err
	}
	snapshot.ServerID = server.ID
	if err := db.RetryOnBusy(ctx, func() error {
		_, appendErr := r.monitorRepo.Append(ctx, snapshot)
		return appendErr
	}); err != nil {
		return "", err
	}

	preferredInterface := strings.TrimSpace(credentials.TrafficInterface)
	if preferredInterface == "" {
		if latestTraffic, err := r.trafficRepo.GetLatestTrafficByServer(ctx, server.ID); err == nil {
			preferredInterface = latestTraffic.InterfaceName
		}
	}

	trafficSnapshot, _, trafficErr := monitoring.CollectTraffic(ctx, r.ssh, req, preferredInterface)
	trafficStored := false
	if trafficErr == nil {
		trafficSnapshot.ServerID = server.ID
		if err := db.RetryOnBusy(ctx, func() error {
			_, appendErr := r.trafficRepo.AppendTraffic(ctx, trafficSnapshot)
			return appendErr
		}); err == nil {
			trafficStored = true
		}
	}

	// Evaluate alert rules against the values just collected (no extra SSH). This
	// must never fail or block the monitoring job, so errors are only logged.
	r.evaluateAlerts(ctx, server, snapshot, trafficSnapshot, trafficStored && trafficSnapshot.Available)

	switch {
	case trafficStored && trafficSnapshot.Available:
		return "Stored resource and vnStat snapshots.", nil
	case trafficStored:
		return "Stored resource snapshot. vnStat is not available on this server yet.", nil
	default:
		return "Stored resource snapshot. vnStat refresh did not complete.", nil
	}
}

// evaluateAlerts runs the alert evaluator against the freshly collected values.
// Evaluation errors are logged and never propagate to the monitoring job.
func (r *Runtime) evaluateAlerts(ctx context.Context, server servers.Server, snapshot monitoring.Snapshot, traffic monitoring.TrafficSnapshot, trafficAvailable bool) {
	if r.evaluator == nil {
		return
	}

	metrics := alerts.Metrics{
		CPU:              snapshot.CPUUsage,
		RAM:              snapshot.RAMUsage,
		Disk:             snapshot.DiskUsage,
		Load1:            snapshot.LoadAverage1,
		Load5:            snapshot.LoadAverage5,
		Load15:           snapshot.LoadAverage15,
		TrafficAvailable: trafficAvailable,
		TrafficTotalGiB:  currentMonthTotalGiB(traffic),
		PeakMbps:         traffic.PeakMbps,
		AvgMbps:          traffic.AvgMbps,
	}

	// Layer in the forecast-derived predictive metrics. These reuse the daily/
	// monthly rows already in hand from the traffic collection above (no extra
	// SSH) plus a single indexed limit lookup, so the only added cost for a server
	// without a limit is that cheap SELECT — and the metrics stay unavailable.
	metrics.ForecastAvailable, metrics.ProjectedExceedsLimit, metrics.DaysToExhaustion =
		r.forecastMetrics(ctx, server.ID, traffic, trafficAvailable)

	if err := r.evaluator.Evaluate(ctx, alerts.Target{ID: server.ID, Name: server.Name}, metrics); err != nil {
		slog.Warn("alert evaluation failed",
			slog.Int64("server_id", server.ID),
			slog.String("error", err.Error()),
		)
	}
}

const bytesPerGiB = 1024 * 1024 * 1024

// currentMonthTotalGiB returns the current calendar month's total traffic in
// GiB from the vnStat monthly rows, or 0 when the row is absent.
func currentMonthTotalGiB(traffic monitoring.TrafficSnapshot) float64 {
	label := time.Now().UTC().Format("2006-01")
	for _, row := range traffic.MonthlyRows {
		if row.Label == label {
			return float64(row.TotalBytes) / bytesPerGiB
		}
	}
	return 0
}

// forecastMetrics derives the predictive alert metrics for a server from the
// freshly collected traffic snapshot. It returns available=false (the metrics
// are then skipped by the evaluator) whenever the forecast cannot be trusted:
// no forecast service, traffic unavailable this cycle, no monthly limit
// configured, or no daily history to project from. When available, it reuses the
// SAME analytics.ForecastService the analytics page uses, so the alert and the
// page can never disagree on the projection.
//
// days is 0 when already over, the projected days-remaining when on track to
// exhaust, and alerts.DaysToExhaustionSafe when not projected to exhaust this
// month (so a "≤ N days" rule resolves rather than getting stuck).
func (r *Runtime) forecastMetrics(ctx context.Context, serverID int64, traffic monitoring.TrafficSnapshot, trafficAvailable bool) (available, projectedOver bool, days float64) {
	if r.forecastSvc == nil || r.analyticsRepo == nil || !trafficAvailable {
		return false, false, 0
	}

	limitBytes, ok, err := r.analyticsRepo.GetTrafficLimit(ctx, serverID)
	if err != nil || !ok || limitBytes <= 0 {
		// No limit configured (the common case): predictive metrics are unavailable
		// so servers without a limit never trigger them.
		return false, false, 0
	}

	days30 := toAnalyticsDays(traffic.DailyRows)
	if len(days30) == 0 {
		// A limit is set but there is no history yet to forecast against.
		return false, false, 0
	}

	out := r.forecastSvc.Compute(days30, toAnalyticsMonths(traffic.MonthlyRows), limitBytes)
	available, projectedOver, days = forecastAlertValues(out)
	return available, projectedOver, days
}

// forecastAlertValues maps a forecast output onto the predictive metric values.
// It is split out as a pure function so the mapping is unit-testable without a
// scheduler, DB, or clock.
func forecastAlertValues(out analytics.ForecastOutput) (available, projectedOver bool, days float64) {
	if !out.Exhaustion.HasLimit {
		return false, false, 0
	}
	projectedOver = out.Risks.Exhaustion || out.Exhaustion.AlreadyOver
	switch {
	case out.Exhaustion.AlreadyOver:
		days = 0
	case out.Exhaustion.WillExhaust:
		days = float64(out.Exhaustion.DaysRemaining)
	default:
		days = alerts.DaysToExhaustionSafe
	}
	return true, projectedOver, days
}

// toAnalyticsDays / toAnalyticsMonths convert the monitoring traffic rows into
// the analytics forecast inputs, mirroring analytics.SQLRepository's own
// conversion (total defaults to RX+TX when the stored total is zero).
func toAnalyticsDays(rows []monitoring.TrafficRow) []analytics.TrafficDay {
	out := make([]analytics.TrafficDay, 0, len(rows))
	for _, row := range rows {
		total := row.TotalBytes
		if total == 0 {
			total = row.RXBytes + row.TXBytes
		}
		out = append(out, analytics.TrafficDay{Label: row.Label, RX: row.RXBytes, TX: row.TXBytes, Total: total})
	}
	return out
}

func toAnalyticsMonths(rows []monitoring.TrafficRow) []analytics.TrafficMonth {
	out := make([]analytics.TrafficMonth, 0, len(rows))
	for _, row := range rows {
		total := row.TotalBytes
		if total == 0 {
			total = row.RXBytes + row.TXBytes
		}
		out = append(out, analytics.TrafficMonth{Label: row.Label, RX: row.RXBytes, TX: row.TXBytes, Total: total})
	}
	return out
}

func (r *Runtime) executeNodesJob(ctx context.Context, server servers.Server, req sshclient.CommandRequest) (string, error) {
	snapshots, probes, err := nodes.Collect(ctx, r.ssh, req, r.providers)
	if err != nil {
		return "", err
	}

	collectedAt := latestNodesCollectedAt(snapshots, probes)
	if err := db.RetryOnBusy(ctx, func() error {
		return r.nodeRepo.ReplaceLatest(ctx, server.ID, snapshots, collectedAt)
	}); err != nil {
		return "", err
	}
	if len(snapshots) == 0 {
		return "Stored an empty discovery batch after no detector matched.", nil
	}
	return fmt.Sprintf("Stored %d node discovery snapshots.", len(snapshots)), nil
}

func (r *Runtime) intervalFor(jobType JobType) time.Duration {
	switch jobType {
	case JobNodes:
		return r.cfg.NodesInterval
	default:
		return r.cfg.MonitoringInterval
	}
}

func (r *Runtime) ensureState(server servers.Server, jobType JobType) *jobState {
	key := jobKey(server.ID, jobType)

	r.mu.Lock()
	defer r.mu.Unlock()

	state, ok := r.jobs[key]
	if !ok {
		state = &jobState{
			ServerID:   server.ID,
			ServerName: server.Name,
			JobType:    jobType,
		}
		r.jobs[key] = state
	}
	return state
}

func latestNodesCollectedAt(snapshots []nodes.Snapshot, probes []nodes.ProbeReport) time.Time {
	for _, snapshot := range snapshots {
		if !snapshot.CollectedAt.IsZero() {
			return snapshot.CollectedAt
		}
	}
	for _, probe := range probes {
		if !probe.Result.CompletedAt.IsZero() {
			return probe.Result.CompletedAt
		}
	}
	return time.Now().UTC()
}

func jobKey(serverID int64, jobType JobType) string {
	return fmt.Sprintf("%d:%s", serverID, jobType)
}

func (s *jobState) status() string {
	if s == nil {
		return "unknown"
	}
	if s.Running {
		return "running"
	}
	if !s.Allowed {
		return "blocked"
	}
	if s.LastError != "" && s.ConsecutiveFailures > 0 {
		return "retrying"
	}
	if !s.LastSuccessAt.IsZero() {
		return "scheduled"
	}
	return "pending"
}

func parseCredentialReference(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("external_ref is empty")
	}

	out := map[string]string{}
	for _, raw := range splitReferenceParts(value) {
		key, current, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("external_ref segment %q must use key=value", raw)
		}
		key = strings.TrimSpace(strings.ToLower(key))
		current = strings.TrimSpace(current)
		if key == "" || current == "" {
			return nil, fmt.Errorf("external_ref segment %q must include a non-empty key and value", raw)
		}
		switch key {
		case "password_env", "password_file", "key_env", "key_file", "passphrase_env", "passphrase_file", "traffic_interface":
		default:
			return nil, fmt.Errorf("external_ref key %q is not supported by the scheduler", key)
		}
		out[key] = current
	}
	return out, nil
}

func resolveCredential(authMode string, reference string) (resolvedCredential, error) {
	values, err := parseCredentialReference(reference)
	if err != nil {
		return resolvedCredential{}, err
	}

	resolved := resolvedCredential{
		TrafficInterface: strings.TrimSpace(values["traffic_interface"]),
	}
	if envKey := values["password_env"]; envKey != "" {
		resolved.Password = strings.TrimSpace(os.Getenv(envKey))
	}
	if filePath := values["password_file"]; filePath != "" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return resolvedCredential{}, fmt.Errorf("read password_file %q: %w", filePath, err)
		}
		resolved.Password = strings.TrimSpace(string(content))
	}
	if envKey := values["key_env"]; envKey != "" {
		resolved.PrivateKeyPEM = strings.TrimSpace(os.Getenv(envKey))
	}
	if filePath := values["key_file"]; filePath != "" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return resolvedCredential{}, fmt.Errorf("read key_file %q: %w", filePath, err)
		}
		resolved.PrivateKeyPEM = strings.TrimSpace(string(content))
	}
	if envKey := values["passphrase_env"]; envKey != "" {
		resolved.KeyPassphrase = strings.TrimSpace(os.Getenv(envKey))
	}
	if filePath := values["passphrase_file"]; filePath != "" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return resolvedCredential{}, fmt.Errorf("read passphrase_file %q: %w", filePath, err)
		}
		resolved.KeyPassphrase = strings.TrimSpace(string(content))
	}

	switch strings.TrimSpace(authMode) {
	case servers.AuthModePassword:
		if resolved.Password == "" {
			return resolvedCredential{}, fmt.Errorf("password auth requires password_env or password_file in external_ref")
		}
	case servers.AuthModeKey:
		if resolved.PrivateKeyPEM == "" {
			return resolvedCredential{}, fmt.Errorf("key auth requires key_env or key_file in external_ref")
		}
	case servers.AuthModeHybrid:
		if resolved.Password == "" && resolved.PrivateKeyPEM == "" {
			return resolvedCredential{}, fmt.Errorf("hybrid auth requires password_* or key_* in external_ref")
		}
	default:
		if resolved.Password == "" && resolved.PrivateKeyPEM == "" {
			return resolvedCredential{}, fmt.Errorf("scheduler could not resolve any SSH credential from external_ref")
		}
	}

	return resolved, nil
}

func splitReferenceParts(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	})

	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		out = append(out, field)
	}
	return out
}
