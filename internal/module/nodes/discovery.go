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

// Collect runs every provider's discovery probe over SSH and aggregates the
// parsed node snapshots.  One probe failing (e.g. a transient SSH error) does
// not abort the run — the per-probe error is reported alongside the results.
func Collect(ctx context.Context, sshService *sshclient.Service, req sshclient.CommandRequest, providers []Provider) ([]Snapshot, []ProbeReport, error) {
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
		if collectedAt.IsZero() && !result.CompletedAt.IsZero() {
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
		snapshots = append(snapshots, provider.ParseDiscovery(result.Stdout, result.CompletedAt)...)
	}

	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	for i := range snapshots {
		if snapshots[i].CollectedAt.IsZero() {
			snapshots[i].CollectedAt = collectedAt
		}
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
