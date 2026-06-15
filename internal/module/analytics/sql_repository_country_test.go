package analytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

// TestServerSummariesCarryCountryCode verifies that the overview queries fold
// each server's detected country code into the row, and leave it empty for a
// server without a detected country — so the template degrades gracefully.
func TestServerSummariesCarryCountryCode(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()

	serverRepo := servers.NewSQLRepository(runtime.SQL)
	withCountry, err := serverRepo.Create(ctx, servers.Server{
		Name:               "tokyo-1",
		Host:               "203.0.113.10",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create(withCountry) error = %v", err)
	}
	if err := serverRepo.UpdateCountry(ctx, withCountry.ID, "JP", "Japan"); err != nil {
		t.Fatalf("UpdateCountry() error = %v", err)
	}

	noCountry, err := serverRepo.Create(ctx, servers.Server{
		Name:               "private-1",
		Host:               "10.0.0.5",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create(noCountry) error = %v", err)
	}

	// One metric snapshot and one current-month vnstat snapshot per server.
	month := time.Now().UTC().Format("2006-01")
	monthlyJSON := `[{"label":"` + month + `","rx_bytes":100,"tx_bytes":200,"total_bytes":300}]`
	for _, id := range []int64{withCountry.ID, noCountry.ID} {
		if _, err := runtime.SQL.ExecContext(ctx,
			`INSERT INTO system_snapshots (server_id, cpu_usage, ram_usage, disk_usage) VALUES (?, ?, ?, ?)`,
			id, 50.0, 40.0, 30.0,
		); err != nil {
			t.Fatalf("insert system_snapshots: %v", err)
		}
		if _, err := runtime.SQL.ExecContext(ctx,
			`INSERT INTO vnstat_snapshots (server_id, available, monthly_rows_json) VALUES (?, 1, ?)`,
			id, monthlyJSON,
		); err != nil {
			t.Fatalf("insert vnstat_snapshots: %v", err)
		}
	}

	repo := analytics.NewSQLRepository(runtime.SQL)

	metrics, err := repo.ListServerMetricSummaries(ctx, 10)
	if err != nil {
		t.Fatalf("ListServerMetricSummaries() error = %v", err)
	}
	assertCountryCode(t, "metrics", collectMetricCountries(metrics), withCountry.ID, noCountry.ID)

	traffic, err := repo.ListServerTrafficSummaries(ctx, 10)
	if err != nil {
		t.Fatalf("ListServerTrafficSummaries() error = %v", err)
	}
	assertCountryCode(t, "traffic", collectTrafficCountries(traffic), withCountry.ID, noCountry.ID)
}

func collectMetricCountries(rows []analytics.ServerMetricSummary) map[int64]string {
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.ServerID] = r.CountryCode
	}
	return out
}

func collectTrafficCountries(rows []analytics.ServerTrafficSummary) map[int64]string {
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.ServerID] = r.CountryCode
	}
	return out
}

func assertCountryCode(t *testing.T, label string, codes map[int64]string, withCountryID, noCountryID int64) {
	t.Helper()
	if got := codes[withCountryID]; got != "JP" {
		t.Errorf("%s: server with country code = %q, want %q", label, got, "JP")
	}
	if got := codes[noCountryID]; got != "" {
		t.Errorf("%s: server without country code = %q, want empty", label, got)
	}
}
