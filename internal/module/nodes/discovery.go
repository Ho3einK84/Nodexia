package nodes

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

// ProbeReport records the result of one provider discovery probe.
type ProbeReport struct {
	Label   string
	Command string
	Result  sshclient.CommandResult
	Error   error
}

type commandRunner interface {
	RunCommand(context.Context, sshclient.CommandRequest) (sshclient.CommandResult, error)
}

// versionResolvingProvider is an optional provider capability. PasarGuard uses
// it to perform a follow-up Docker-log probe after its general discovery output
// has revealed the real container names. Rebecca intentionally does not
// implement it; its existing .binary-release.json path remains unchanged.
type versionResolvingProvider interface {
	VersionDiscoveryCommand(discoveryOutput string) string
	ResolveVersions(snapshots []Snapshot, versionOutput string) []Snapshot
}

// Collect runs every provider's discovery probe over SSH and aggregates the
// parsed node snapshots.  One probe failing (e.g. a transient SSH error) does
// not abort the run — the per-probe error is reported alongside the results.
func Collect(ctx context.Context, sshService *sshclient.Service, req sshclient.CommandRequest, providers []Provider) ([]Snapshot, []ProbeReport, error) {
	return collect(ctx, sshService, req, providers)
}

func collect(ctx context.Context, sshService commandRunner, req sshclient.CommandRequest, providers []Provider) ([]Snapshot, []ProbeReport, error) {
	if len(providers) == 0 {
		providers = DefaultProviders()
	}

	snapshots := make([]Snapshot, 0)
	reports := make([]ProbeReport, 0, len(providers))
	var collectedAt time.Time

	for _, provider := range providers {
		command := provider.DiscoveryCommand()
		result, err := sshService.RunCommand(ctx, sshclient.CommandRequest{
			ConnectionRequest: req.ConnectionRequest,
			Command:           command,
			CommandTimeout:    req.CommandTimeout,
		})
		// Track the newest probe time so the whole sweep shares one timestamp.
		if result.CompletedAt.After(collectedAt) {
			collectedAt = result.CompletedAt
		}
		reports = append(reports, ProbeReport{
			Label:   provider.Type(),
			Command: command,
			Result:  result,
			Error:   err,
		})
		if err != nil {
			continue
		}

		providerSnapshots := provider.ParseDiscovery(result.Stdout, result.CompletedAt)
		if resolver, ok := provider.(versionResolvingProvider); ok {
			versionCommand := resolver.VersionDiscoveryCommand(result.Stdout)
			if versionCommand != "" {
				versionResult, versionErr := sshService.RunCommand(ctx, sshclient.CommandRequest{
					ConnectionRequest: req.ConnectionRequest,
					Command:           versionCommand,
					CommandTimeout:    req.CommandTimeout,
				})
				if versionResult.CompletedAt.After(collectedAt) {
					collectedAt = versionResult.CompletedAt
				}
				reports = append(reports, ProbeReport{
					Label:   provider.Type() + "-version",
					Command: versionCommand,
					Result:  versionResult,
					Error:   versionErr,
				})
				// A transport error can still leave complete stdout for one or
				// more containers. The parser accepts only exact startup lines,
				// so applying valid partial results is safe; every other node
				// simply retains its image-tag/unknown fallback.
				providerSnapshots = resolver.ResolveVersions(providerSnapshots, versionResult.Stdout)
			}
		}
		snapshots = append(snapshots, providerSnapshots...)
	}

	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	// Every node found in this sweep MUST carry the same collectedAt: providers
	// run as separate SSH probes that complete at different instants, and the
	// repository groups "the latest snapshot" by a single created_at. Without
	// this, PasarGuard and Rebecca nodes would land under different timestamps
	// and only one family would ever be listed.
	for i := range snapshots {
		snapshots[i].CollectedAt = collectedAt
	}

	return dedupeSnapshots(snapshots), reports, nil
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
