package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
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
