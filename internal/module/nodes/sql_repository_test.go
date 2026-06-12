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
