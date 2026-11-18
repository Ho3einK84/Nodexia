package alerts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

func ptr(v int64) *int64 { return &v }

func newTestServer(t *testing.T, ctx context.Context, runtime *db.Runtime, name string) int64 {
	t.Helper()
	repo := servers.NewSQLRepository(runtime.SQL)
	server, err := repo.Create(ctx, servers.Server{
		Name:               name,
		Host:               "10.0.0.1",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("create server %q: %v", name, err)
	}
	return server.ID
}

func TestRuleCRUD(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	repo := alerts.NewSQLRepository(runtime.SQL)

	serverID := newTestServer(t, ctx, runtime, "lab-1")
	channel, err := repo.CreateChannel(ctx, alerts.Channel{
		Kind: alerts.ChannelKindTelegram, Name: "Ops", ChatID: "-100", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}

	created, err := repo.CreateRule(ctx, alerts.Rule{
		ServerID:        ptr(serverID),
		Metric:          alerts.MetricCPU,
		Comparator:      alerts.ComparatorGT,
		Threshold:       80,
		ConsecutiveHits: 2,
		CooldownSeconds: 600,
		Severity:        alerts.SeverityCritical,
		ChannelID:       ptr(channel.ID),
		Enabled:         true,
		Note:            "watch cpu",
	})
	if err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}
	if created.ID < 1 {
		t.Fatalf("CreateRule() id = %d", created.ID)
	}

	fetched, err := repo.GetRule(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRule() error = %v", err)
	}
	if fetched.ServerID == nil || *fetched.ServerID != serverID {
		t.Fatalf("ServerID = %v, want %d", fetched.ServerID, serverID)
	}
	if fetched.ChannelID == nil || *fetched.ChannelID != channel.ID {
		t.Fatalf("ChannelID = %v, want %d", fetched.ChannelID, channel.ID)
	}
	if fetched.Threshold != 80 || fetched.ConsecutiveHits != 2 || fetched.CooldownSeconds != 600 {
		t.Fatalf("unexpected rule values: %#v", fetched)
	}
	if !fetched.Enabled || fetched.Severity != alerts.SeverityCritical {
		t.Fatalf("unexpected enabled/severity: %#v", fetched)
	}

	list, err := repo.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListRules() len = %d, want 1", len(list))
	}

	// Promote the rule to a global rule and disable it.
	fetched.ServerID = nil
	fetched.ChannelID = nil
	fetched.Threshold = 95
	fetched.Enabled = false
	updated, err := repo.UpdateRule(ctx, fetched)
	if err != nil {
		t.Fatalf("UpdateRule() error = %v", err)
	}
	if !updated.IsGlobal() {
		t.Fatal("expected updated rule to be global")
	}
	if updated.ChannelID != nil {
		t.Fatalf("ChannelID = %v, want nil", updated.ChannelID)
	}
	if updated.Threshold != 95 || updated.Enabled {
		t.Fatalf("unexpected updated values: %#v", updated)
	}

	if err := repo.DeleteRule(ctx, created.ID); err != nil {
		t.Fatalf("DeleteRule() error = %v", err)
	}
	if _, err := repo.GetRule(ctx, created.ID); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("GetRule() after delete error = %v, want ErrNotFound", err)
	}
}

func TestListEnabledRulesForServer(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	repo := alerts.NewSQLRepository(runtime.SQL)

	server1 := newTestServer(t, ctx, runtime, "lab-1")
	server2 := newTestServer(t, ctx, runtime, "lab-2")

	mustRule := func(rule alerts.Rule) {
		t.Helper()
		if _, err := repo.CreateRule(ctx, rule); err != nil {
			t.Fatalf("CreateRule() error = %v", err)
		}
	}

	mustRule(alerts.Rule{Metric: alerts.MetricRAM, Threshold: 90, Enabled: true})                           // global, enabled
	mustRule(alerts.Rule{ServerID: ptr(server1), Metric: alerts.MetricCPU, Threshold: 80, Enabled: true})   // server1, enabled
	mustRule(alerts.Rule{ServerID: ptr(server1), Metric: alerts.MetricDisk, Threshold: 95, Enabled: false}) // server1, disabled
	mustRule(alerts.Rule{ServerID: ptr(server2), Metric: alerts.MetricLoad1, Threshold: 4, Enabled: true})  // server2, enabled

	rules, err := repo.ListEnabledRulesForServer(ctx, server1)
	if err != nil {
		t.Fatalf("ListEnabledRulesForServer() error = %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2 (global + server1 enabled): %#v", len(rules), rules)
	}

	metrics := map[string]bool{}
	for _, rule := range rules {
		metrics[rule.Metric] = true
	}
	if !metrics[alerts.MetricRAM] || !metrics[alerts.MetricCPU] {
		t.Fatalf("expected ram (global) and cpu (server1), got %#v", metrics)
	}
	if metrics[alerts.MetricDisk] {
		t.Fatal("disabled rule should not be returned")
	}
	if metrics[alerts.MetricLoad1] {
		t.Fatal("other server's rule should not be returned")
	}
}

func TestChannelDeleteDetachesRules(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	repo := alerts.NewSQLRepository(runtime.SQL)

	channel, err := repo.CreateChannel(ctx, alerts.Channel{
		Kind: alerts.ChannelKindTelegram, Name: "Ops", ChatID: "-100", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}

	rule, err := repo.CreateRule(ctx, alerts.Rule{
		Metric: alerts.MetricCPU, Threshold: 90, ChannelID: ptr(channel.ID), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}

	if err := repo.DeleteChannel(ctx, channel.ID); err != nil {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
	if _, err := repo.GetChannel(ctx, channel.ID); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("GetChannel() after delete = %v, want ErrNotFound", err)
	}

	reloaded, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule() error = %v", err)
	}
	if reloaded.ChannelID != nil {
		t.Fatalf("ChannelID = %v, want nil after channel delete", reloaded.ChannelID)
	}

	enabled, err := repo.ListEnabledChannels(ctx)
	if err != nil {
		t.Fatalf("ListEnabledChannels() error = %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("ListEnabledChannels() len = %d, want 0", len(enabled))
	}
}

func TestSilenceLifecycle(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	repo := alerts.NewSQLRepository(runtime.SQL)

	server1 := newTestServer(t, ctx, runtime, "lab-1")
	server2 := newTestServer(t, ctx, runtime, "lab-2")

	cpuSilence, err := repo.CreateSilence(ctx, alerts.Silence{
		ServerID: server1, Metric: alerts.MetricCPU, Reason: "noisy",
	})
	if err != nil {
		t.Fatalf("CreateSilence() error = %v", err)
	}

	silenced, err := repo.IsSilenced(ctx, server1, alerts.MetricCPU)
	if err != nil {
		t.Fatalf("IsSilenced() error = %v", err)
	}
	if !silenced {
		t.Fatal("expected cpu to be silenced for server1")
	}

	// A different metric is not yet silenced.
	if silenced, _ := repo.IsSilenced(ctx, server1, alerts.MetricRAM); silenced {
		t.Fatal("expected ram not silenced before wildcard")
	}

	// The "all" wildcard mutes every metric for the server.
	if _, err := repo.CreateSilence(ctx, alerts.Silence{ServerID: server1, Metric: alerts.MetricAll}); err != nil {
		t.Fatalf("CreateSilence(all) error = %v", err)
	}
	if silenced, _ := repo.IsSilenced(ctx, server1, alerts.MetricRAM); !silenced {
		t.Fatal("expected ram silenced via 'all' wildcard")
	}

	forServer, err := repo.ListSilencesForServer(ctx, server1)
	if err != nil {
		t.Fatalf("ListSilencesForServer() error = %v", err)
	}
	if len(forServer) != 2 {
		t.Fatalf("ListSilencesForServer() len = %d, want 2", len(forServer))
	}

	// Re-silencing the same (server, metric) upserts rather than duplicating.
	if _, err := repo.CreateSilence(ctx, alerts.Silence{ServerID: server1, Metric: alerts.MetricCPU, Reason: "still noisy"}); err != nil {
		t.Fatalf("CreateSilence(upsert) error = %v", err)
	}
	if again, _ := repo.ListSilencesForServer(ctx, server1); len(again) != 2 {
		t.Fatalf("after upsert len = %d, want 2", len(again))
	}

	// An expired silence is not honored.
	past := time.Now().Add(-time.Hour)
	if _, err := repo.CreateSilence(ctx, alerts.Silence{ServerID: server2, Metric: alerts.MetricCPU, ExpiresAt: &past}); err != nil {
		t.Fatalf("CreateSilence(expired) error = %v", err)
	}
	if silenced, _ := repo.IsSilenced(ctx, server2, alerts.MetricCPU); silenced {
		t.Fatal("expected expired silence to be ignored")
	}

	// Deleting a silence removes it.
	if err := repo.DeleteSilence(ctx, cpuSilence.ID); err != nil {
		t.Fatalf("DeleteSilence() error = %v", err)
	}
	if remaining, _ := repo.ListSilencesForServer(ctx, server1); len(remaining) != 1 {
		t.Fatalf("after delete len = %d, want 1", len(remaining))
	}
	if _, err := repo.GetSilence(ctx, cpuSilence.ID); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("GetSilence() after delete = %v, want ErrNotFound", err)
	}
}

func TestCountEventsAndListEventsPage(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	repo := alerts.NewSQLRepository(runtime.SQL)
	serverID := newTestServer(t, ctx, runtime, "lab-events")

	if total, err := repo.CountEvents(ctx); err != nil || total != 0 {
		t.Fatalf("CountEvents() = %d, %v; want 0, nil", total, err)
	}

	// Seed 12 events with distinct observed values 1..12 (ids ascend with i).
	for i := 1; i <= 12; i++ {
		_, err := repo.CreateEvent(ctx, alerts.Event{
			ServerID:      serverID,
			Metric:        alerts.MetricCPU,
			ObservedValue: float64(i),
			Threshold:     90,
			Severity:      alerts.SeverityWarning,
		})
		if err != nil {
			t.Fatalf("CreateEvent(%d) error = %v", i, err)
		}
	}

	total, err := repo.CountEvents(ctx)
	if err != nil {
		t.Fatalf("CountEvents() error = %v", err)
	}
	if total != 12 {
		t.Fatalf("CountEvents() = %d, want 12", total)
	}

	// Page 1: newest first → observed values 12..3.
	page1, err := repo.ListEventsPage(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListEventsPage(10, 0) error = %v", err)
	}
	if len(page1) != 10 {
		t.Fatalf("page 1 length = %d, want 10", len(page1))
	}
	if page1[0].ObservedValue != 12 || page1[9].ObservedValue != 3 {
		t.Errorf("page 1 order = %v..%v, want 12..3", page1[0].ObservedValue, page1[9].ObservedValue)
	}

	// Page 2: the remaining two oldest events.
	page2, err := repo.ListEventsPage(ctx, 10, 10)
	if err != nil {
		t.Fatalf("ListEventsPage(10, 10) error = %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2 length = %d, want 2", len(page2))
	}
	if page2[0].ObservedValue != 2 || page2[1].ObservedValue != 1 {
		t.Errorf("page 2 = %v, %v; want 2, 1", page2[0].ObservedValue, page2[1].ObservedValue)
	}

	// Past the end → empty, not an error.
	if rest, err := repo.ListEventsPage(ctx, 10, 20); err != nil || len(rest) != 0 {
		t.Errorf("ListEventsPage(10, 20) = %d rows, %v; want 0, nil", len(rest), err)
	}
}
