package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

func TestSQLRepositoryReplaceAndGetLatest(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	if _, err := runtime.SQL.ExecContext(
		ctx,
		`INSERT INTO servers (name, host, port, auth_mode, username) VALUES (?, ?, ?, ?, ?)`,
		"test", "192.0.2.10", 22, "password", "root",
	); err != nil {
		t.Fatalf("insert server: %v", err)
	}

	repo := nodes.NewSQLRepository(runtime.SQL)
	collectedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	snapshots := []nodes.Snapshot{
		{
			NodeType:     "pasarguard-node",
			ServiceName:  "node2",
			InstallMode:  "docker",
			Version:      "latest",
			HealthStatus: "running",
			ActivePorts:  []string{"62050"},
			ServicePort:  "62050",
			Protocol:     "grpc",
			DataDir:      "/var/lib/node2",
			Confidence:   "high",
			Evidence:     []string{"Config: /opt/node2/.env"},
			CollectedAt:  collectedAt,
		},
	}

	if err := repo.ReplaceLatest(ctx, 1, snapshots, collectedAt); err != nil {
		t.Fatalf("ReplaceLatest: %v", err)
	}

	stored, err := repo.GetLatestByServer(ctx, 1)
	if err != nil {
		t.Fatalf("GetLatestByServer: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("len(stored) = %d, want 1", len(stored))
	}
	got := stored[0]
	if got.ServiceName != "node2" {
		t.Errorf("ServiceName = %q, want node2", got.ServiceName)
	}
	if got.DataDir != "/var/lib/node2" {
		t.Errorf("DataDir = %q, want /var/lib/node2", got.DataDir)
	}
	if got.HealthStatus != "running" {
		t.Errorf("HealthStatus = %q, want running", got.HealthStatus)
	}

	hasAny, err := repo.HasAny(ctx, 1)
	if err != nil || !hasAny {
		t.Fatalf("HasAny = %v, %v; want true, nil", hasAny, err)
	}
}

// TestSQLRepositoryMixedTimestampsStaySingleBatch reproduces the listing bug:
// providers run as separate probes that finish at different instants, so the
// snapshots arrive carrying different CollectedAt values. ReplaceLatest must
// collapse them onto the single batch timestamp, otherwise GetLatestByServer
// (which groups by one created_at) returns only one node family.
func TestSQLRepositoryMixedTimestampsStaySingleBatch(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	if _, err := runtime.SQL.ExecContext(
		ctx,
		`INSERT INTO servers (name, host, port, auth_mode, username) VALUES (?, ?, ?, ?, ?)`,
		"mixed", "192.0.2.20", 22, "password", "root",
	); err != nil {
		t.Fatalf("insert server: %v", err)
	}

	repo := nodes.NewSQLRepository(runtime.SQL)
	batchTime := time.Date(2026, 6, 13, 4, 0, 0, 0, time.UTC)

	// Two PasarGuard instances and one Rebecca node, each stamped with a
	// DIFFERENT time (as the live Collect path used to do).
	snapshots := []nodes.Snapshot{
		{NodeType: "pasarguard-node", ServiceName: "node", InstallMode: "docker", HealthStatus: "running", CollectedAt: batchTime.Add(-2 * time.Second)},
		{NodeType: "pasarguard-node", ServiceName: "node2", InstallMode: "docker", HealthStatus: "stopped", CollectedAt: batchTime.Add(-1 * time.Second)},
		{NodeType: "rebecca-node", ServiceName: "rebecca-node", InstallMode: "binary", HealthStatus: "running", CollectedAt: batchTime},
	}

	if err := repo.ReplaceLatest(ctx, 1, snapshots, batchTime); err != nil {
		t.Fatalf("ReplaceLatest: %v", err)
	}

	stored, err := repo.GetLatestByServer(ctx, 1)
	if err != nil {
		t.Fatalf("GetLatestByServer: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("len(stored) = %d, want 3 (all nodes, both families)", len(stored))
	}

	types := map[string]int{}
	for _, s := range stored {
		types[s.NodeType]++
		if !s.CollectedAt.Equal(batchTime) {
			t.Errorf("snapshot %s/%s CollectedAt = %s, want unified %s", s.NodeType, s.ServiceName, s.CollectedAt, batchTime)
		}
	}
	if types["pasarguard-node"] != 2 || types["rebecca-node"] != 1 {
		t.Fatalf("type counts = %v, want 2 pasarguard + 1 rebecca", types)
	}
}

// TestListLatestNodeStatus checks the fleet summary counts only real nodes from
// each server's latest batch and folds health into running/stopped/other.
func TestListLatestNodeStatus(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := runtime.SQL.ExecContext(ctx,
			`INSERT INTO servers (name, host, port, auth_mode, username) VALUES (?, ?, ?, ?, ?)`,
			name, "192.0.2.1", 22, "password", "root",
		); err != nil {
			t.Fatalf("insert server %s: %v", name, err)
		}
	}

	repo := nodes.NewSQLRepository(runtime.SQL)
	batch := time.Date(2026, 6, 20, 4, 0, 0, 0, time.UTC)

	// alpha (id 1): one running + one stopped node.
	if err := repo.ReplaceLatest(ctx, 1, []nodes.Snapshot{
		{NodeType: "pasarguard-node", ServiceName: "node", HealthStatus: "running"},
		{NodeType: "pasarguard-node", ServiceName: "node2", HealthStatus: "stopped"},
	}, batch); err != nil {
		t.Fatalf("ReplaceLatest alpha: %v", err)
	}
	// beta (id 2): a single running node.
	if err := repo.ReplaceLatest(ctx, 2, []nodes.Snapshot{
		{NodeType: "rebecca-node", ServiceName: "rebecca-node", HealthStatus: "running"},
	}, batch); err != nil {
		t.Fatalf("ReplaceLatest beta: %v", err)
	}
	// gamma (id 3): no node detected — the placeholder must not count.
	if err := repo.ReplaceLatest(ctx, 3, nil, batch); err != nil {
		t.Fatalf("ReplaceLatest gamma: %v", err)
	}

	statuses, err := repo.ListLatestNodeStatus(ctx)
	if err != nil {
		t.Fatalf("ListLatestNodeStatus: %v", err)
	}

	got := map[int64]nodes.ServerNodeStatus{}
	for _, s := range statuses {
		got[s.ServerID] = s
	}
	if a := got[1]; a.Total != 2 || a.Running != 1 || a.Stopped != 1 {
		t.Fatalf("alpha = %+v, want Total 2 / Running 1 / Stopped 1", a)
	}
	if b := got[2]; b.Total != 1 || b.Running != 1 || b.Stopped != 0 {
		t.Fatalf("beta = %+v, want Total 1 / Running 1 / Stopped 0", b)
	}
	if g := got[3]; g.Total != 0 {
		t.Fatalf("gamma Total = %d, want 0 (placeholder excluded)", g.Total)
	}
}

// TestUptimeStatsFromReplaceLatest verifies that every ReplaceLatest sweep
// records one observation per real node (placeholders excluded) and that
// UptimeStats aggregates them into running/total counts.
func TestUptimeStatsFromReplaceLatest(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	srv, err := servers.NewSQLRepository(runtime.SQL).Create(ctx, servers.Server{
		Name: "uptime-host", Host: "203.0.113.50", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create server: %v", err)
	}

	repo := nodes.NewSQLRepository(runtime.SQL)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	// Sweep 1: node running. Sweep 2: node stopped. Sweep 3: running again.
	statuses := []string{"running", "stopped", "running"}
	for i, health := range statuses {
		if err := repo.ReplaceLatest(ctx, srv.ID, []nodes.Snapshot{{
			NodeType: "pasarguard", ServiceName: "node", HealthStatus: health,
		}}, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("ReplaceLatest sweep %d: %v", i, err)
		}
	}
	// A sweep with no nodes stores a placeholder — it must NOT count as an
	// observation of the node.
	if err := repo.ReplaceLatest(ctx, srv.ID, nil, base.Add(10*time.Minute)); err != nil {
		t.Fatalf("ReplaceLatest placeholder: %v", err)
	}

	stats, err := repo.UptimeStats(ctx, srv.ID, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("UptimeStats: %v", err)
	}
	stat, ok := stats[nodes.UptimeKey("pasarguard", "node")]
	if !ok {
		t.Fatalf("no stats recorded; got %#v", stats)
	}
	if stat.Checks != 3 || stat.Running != 2 {
		t.Fatalf("stats = %+v, want Checks=3 Running=2", stat)
	}
}
