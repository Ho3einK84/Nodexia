package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

type queuedCommandResult struct {
	result sshclient.CommandResult
	err    error
}

type queuedCommandRunner struct {
	results  []queuedCommandResult
	commands []string
}

func (r *queuedCommandRunner) RunCommand(_ context.Context, req sshclient.CommandRequest) (sshclient.CommandResult, error) {
	r.commands = append(r.commands, req.Command)
	if len(r.results) == 0 {
		return sshclient.CommandResult{}, errors.New("unexpected command")
	}
	next := r.results[0]
	r.results = r.results[1:]
	return next.result, next.err
}

func TestCollectResolvesPasarguardVersionFromStartupLogs(t *testing.T) {
	discoveredAt := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	versionedAt := discoveredAt.Add(time.Second)
	runner := &queuedCommandRunner{results: []queuedCommandResult{
		{result: sshclient.CommandResult{Stdout: pgDiscoveryFixture, CompletedAt: discoveredAt}},
		{result: sshclient.CommandResult{
			Stdout: `=PGVERSIONS=
pg-node	Starting Node: v0.5.3
=PGVERSIONSEND=`,
			CompletedAt: versionedAt,
		}},
	}}

	snapshots, reports, err := collect(context.Background(), runner, sshclient.CommandRequest{}, []Provider{PasarGuardProvider{}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("RunCommand calls = %d, want discovery + version probe", len(runner.commands))
	}
	if !strings.Contains(runner.commands[1], `docker logs "node" 2>&1`) {
		t.Errorf("version probe must use the linked container name:\n%s", runner.commands[1])
	}
	if len(reports) != 2 || reports[1].Label != pasarguardType+"-version" {
		t.Fatalf("reports = %+v, want PasarGuard discovery + version reports", reports)
	}

	var pgNode Snapshot
	for _, snapshot := range snapshots {
		if snapshot.ServiceName == "pg-node" {
			pgNode = snapshot
			break
		}
	}
	if pgNode.Version != "0.5.3" {
		t.Errorf("pg-node Version = %q, want 0.5.3 from startup log", pgNode.Version)
	}
	if !pgNode.CollectedAt.Equal(versionedAt) {
		t.Errorf("pg-node CollectedAt = %s, want unified latest probe time %s", pgNode.CollectedAt, versionedAt)
	}
}

func TestCollectKeepsVersionFallbackWhenLogProbeFails(t *testing.T) {
	discoveredAt := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	runner := &queuedCommandRunner{results: []queuedCommandResult{
		{result: sshclient.CommandResult{Stdout: pgDiscoveryFixture, CompletedAt: discoveredAt}},
		{err: errors.New("log probe transport failed")},
	}}

	snapshots, reports, err := collect(context.Background(), runner, sshclient.CommandRequest{}, []Provider{PasarGuardProvider{}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(reports) != 2 || reports[1].Error == nil {
		t.Fatalf("version probe failure must be reported without aborting discovery: %+v", reports)
	}

	byName := make(map[string]Snapshot)
	for _, snapshot := range snapshots {
		byName[snapshot.ServiceName] = snapshot
	}
	if got := byName["pg-node"].Version; got != pasarguardUnresolvedVersion {
		t.Errorf("latest-tag node Version = %q, want %q", got, pasarguardUnresolvedVersion)
	}
	if got := byName["node2"].Version; got != "v0.5.0" {
		t.Errorf("pinned-tag node Version = %q, want v0.5.0 fallback", got)
	}
}
