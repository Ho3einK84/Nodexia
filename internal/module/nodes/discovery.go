package nodes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

var (
	serverSweepLocksMu sync.Mutex
	serverSweepLocks   = map[int64]chan struct{}{}
)

func serverSweepSem(serverID int64) chan struct{} {
	serverSweepLocksMu.Lock()
	defer serverSweepLocksMu.Unlock()
	ch, ok := serverSweepLocks[serverID]
	if !ok {
		ch = make(chan struct{}, 1)
		serverSweepLocks[serverID] = ch
	}
	return ch
}

// AcquireServerSweep serializes discovery and snapshot writes per server so
// concurrent scheduler runs, manual refreshes, and post-action sweeps never
// overwrite newer snapshots with older results. Honors ctx cancellation.
func AcquireServerSweep(ctx context.Context, serverID int64) (func(), error) {
	if serverID <= 0 {
		return func() {}, nil
	}
	ch := serverSweepSem(serverID)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PersistDiscoverySweep safely stores discovery snapshots while guaranteeing that
// transient total or partial SSH probe errors never wipe previously persisted
// valid nodes. Partial failures merge newly discovered snapshots with cached
// rows belonging to the failed provider types.
func PersistDiscoverySweep(
	ctx context.Context,
	repo Repository,
	serverID int64,
	snapshots []Snapshot,
	probes []ProbeReport,
	collectErr error,
	source string,
	retryFn func(func() error) error,
) ([]Snapshot, bool, error) {
	if retryFn == nil {
		retryFn = func(fn func() error) error { return fn() }
	}
	collectedAt := latestDiscoveryTime(snapshots, probes)

	if collectErr != nil {
		var dErr *DiscoveryError
		if !errors.As(collectErr, &dErr) || !dErr.Partial {
			return nil, false, collectErr
		}

		failedSet := make(map[string]struct{}, len(dErr.FailedTypes))
		for _, t := range dErr.FailedTypes {
			failedSet[strings.TrimSpace(t)] = struct{}{}
		}
		if existing, getErr := repo.GetLatestByServer(ctx, serverID); getErr == nil {
			for _, oldSnap := range existing {
				if isPlaceholderNodeType(oldSnap.NodeType) {
					continue
				}
				if _, isFailed := failedSet[strings.TrimSpace(oldSnap.NodeType)]; isFailed {
					oldSnap.CollectedAt = collectedAt
					snapshots = append(snapshots, oldSnap)
				}
			}
			snapshots = dedupeSnapshots(snapshots)
		}
		if err := retryFn(func() error {
			return repo.ReplaceLatestWithSource(ctx, serverID, snapshots, collectedAt, source)
		}); err != nil {
			return nil, true, err
		}
		return snapshots, true, collectErr
	}

	if err := retryFn(func() error {
		return repo.ReplaceLatestWithSource(ctx, serverID, snapshots, collectedAt, source)
	}); err != nil {
		return nil, false, err
	}
	return snapshots, false, nil
}

func latestDiscoveryTime(snapshots []Snapshot, probes []ProbeReport) time.Time {
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

// ProbeReport records the result of one provider discovery probe.
type ProbeReport struct {
	Label        string
	ProviderType string
	Command      string
	Result       sshclient.CommandResult
	Error        error
}

// DiscoveryError reports that SSH connection or one or more primary provider
// discovery probes failed. Callers must preserve persisted nodes for any failed
// provider type rather than replacing them with empty/"none" state.
type DiscoveryError struct {
	Partial        bool
	FailedTypes    []string
	SucceededTypes []string
	Err            error
}

func (e *DiscoveryError) Error() string {
	if e == nil || e.Err == nil {
		return "nodes: discovery failed"
	}
	if e.Partial {
		return fmt.Sprintf("nodes: partial discovery failure (%s): %v", strings.Join(e.FailedTypes, ", "), e.Err)
	}
	return fmt.Sprintf("nodes: discovery failed: %v", e.Err)
}

func (e *DiscoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type commandRunner interface {
	RunCommand(context.Context, sshclient.CommandRequest) (sshclient.CommandResult, error)
}

type batchSessionOpener interface {
	OpenBatchSession(context.Context, sshclient.ConnectionRequest) (*sshclient.BatchSession, error)
}

// versionResolvingProvider is an optional provider capability. PasarGuard uses
// it to perform a follow-up Docker-log probe after its general discovery output
// has revealed the real container names.
type versionResolvingProvider interface {
	VersionDiscoveryCommand(discoveryOutput string) string
	ResolveVersions(snapshots []Snapshot, versionOutput string) []Snapshot
}

// Collect runs every provider's discovery probe over a single pooled SSH
// connection (when supported by sshService) and aggregates the parsed node
// snapshots. If connecting or any primary provider probe fails, Collect
// returns a structured error (often *DiscoveryError) so callers never overwrite
// existing cached snapshots with empty/"none" results.
func Collect(ctx context.Context, sshService *sshclient.Service, req sshclient.CommandRequest, providers []Provider) ([]Snapshot, []ProbeReport, error) {
	if sshService == nil {
		return nil, nil, errors.New("nodes: ssh service is required")
	}
	return collect(ctx, sshService, req, providers)
}

func collect(ctx context.Context, runner commandRunner, req sshclient.CommandRequest, providers []Provider) ([]Snapshot, []ProbeReport, error) {
	if len(providers) == 0 {
		providers = DefaultProviders()
	}

	activeRunner := runner
	var latencyMS int
	if opener, ok := runner.(batchSessionOpener); ok {
		batch, err := opener.OpenBatchSession(ctx, req.ConnectionRequest)
		if err != nil {
			reports := make([]ProbeReport, 0, len(providers))
			failed := make([]string, 0, len(providers))
			for _, p := range providers {
				reports = append(reports, ProbeReport{
					Label:        p.Type(),
					ProviderType: p.Type(),
					Command:      p.DiscoveryCommand(),
					Error:        err,
				})
				failed = append(failed, p.Type())
			}
			return nil, reports, &DiscoveryError{
				Partial:     false,
				FailedTypes: failed,
				Err:         err,
			}
		}
		defer func() { _ = batch.Close() }()
		latencyMS = int(batch.RTT().Milliseconds())
		activeRunner = batch
	}

	snapshots := make([]Snapshot, 0)
	reports := make([]ProbeReport, 0, len(providers))
	var (
		collectedAt    time.Time
		primaryErrors  []error
		failedTypes    []string
		succeededTypes []string
	)

	for _, provider := range providers {
		command := provider.DiscoveryCommand()
		result, err := activeRunner.RunCommand(ctx, sshclient.CommandRequest{
			ConnectionRequest: req.ConnectionRequest,
			Command:           command,
			CommandTimeout:    req.CommandTimeout,
		})
		if latencyMS == 0 && result.Duration > 0 {
			latencyMS = int(result.Duration.Milliseconds())
		}
		if result.CompletedAt.After(collectedAt) {
			collectedAt = result.CompletedAt
		}
		reports = append(reports, ProbeReport{
			Label:        provider.Type(),
			ProviderType: provider.Type(),
			Command:      command,
			Result:       result,
			Error:        err,
		})
		if err != nil {
			primaryErrors = append(primaryErrors, fmt.Errorf("%s: %w", provider.Type(), err))
			failedTypes = append(failedTypes, provider.Type())
			continue
		}

		succeededTypes = append(succeededTypes, provider.Type())
		providerSnapshots := provider.ParseDiscovery(result.Stdout, result.CompletedAt)
		if resolver, ok := provider.(versionResolvingProvider); ok {
			versionCommand := resolver.VersionDiscoveryCommand(result.Stdout)
			if versionCommand != "" {
				versionResult, versionErr := activeRunner.RunCommand(ctx, sshclient.CommandRequest{
					ConnectionRequest: req.ConnectionRequest,
					Command:           versionCommand,
					CommandTimeout:    req.CommandTimeout,
				})
				if versionResult.CompletedAt.After(collectedAt) {
					collectedAt = versionResult.CompletedAt
				}
				reports = append(reports, ProbeReport{
					Label:        provider.Type() + "-version",
					ProviderType: provider.Type(),
					Command:      versionCommand,
					Result:       versionResult,
					Error:        versionErr,
				})
				// Secondary version-log failure degrades gracefully to the
				// image-tag / OCI-label / unknown fallback captured above.
				providerSnapshots = resolver.ResolveVersions(providerSnapshots, versionResult.Stdout)
			}
		}
		snapshots = append(snapshots, providerSnapshots...)
	}

	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	collectedAt = collectedAt.UTC()

	for i := range snapshots {
		snapshots[i].CollectedAt = collectedAt
		if snapshots[i].LatencyMS == 0 && latencyMS > 0 {
			snapshots[i].LatencyMS = latencyMS
		}
	}

	deduped := dedupeSnapshots(snapshots)
	if len(primaryErrors) > 0 {
		return deduped, reports, &DiscoveryError{
			Partial:        len(succeededTypes) > 0,
			FailedTypes:    failedTypes,
			SucceededTypes: succeededTypes,
			Err:            errors.Join(primaryErrors...),
		}
	}

	return deduped, reports, nil
}

func dedupeSnapshots(snapshots []Snapshot) []Snapshot {
	if len(snapshots) == 0 {
		return nil
	}
	out := make([]Snapshot, 0, len(snapshots))
	seen := map[string]struct{}{}
	for _, snapshot := range snapshots {
		snapshot = normalizeSnapshot(snapshot)
		key := snapshot.NodeType + "|" + strings.ToLower(snapshot.ServiceName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := snapshotSortRank(out[i].NodeType), snapshotSortRank(out[j].NodeType)
		if ri != rj {
			return ri < rj
		}
		return out[i].ServiceName < out[j].ServiceName
	})
	return out
}

func snapshotSortRank(nodeType string) int {
	switch strings.TrimSpace(nodeType) {
	case pasarguardType:
		return 1
	case rebeccaType:
		return 2
	case "none":
		return 5
	default:
		return 10
	}
}
