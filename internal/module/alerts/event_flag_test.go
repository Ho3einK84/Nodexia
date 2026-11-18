package alerts

import (
	"testing"
	"time"
)

// TestBuildEventRowsFlag verifies that event rows carry the flag emoji for a
// server with a detected country and render nothing for one without — matching
// the graceful degradation of the servers list.
func TestBuildEventRowsFlag(t *testing.T) {
	refs := []serverRef{
		{ID: 1, Name: "tokyo-1", CountryCode: "JP"},
		{ID: 2, Name: "private-1", CountryCode: ""},
	}
	names := serverNameMap(refs)
	countries := serverCountryMap(refs)

	events := []Event{
		{ID: 10, ServerID: 1, Metric: "cpu", Severity: "warning", State: "firing", FiredAt: time.Now()},
		{ID: 11, ServerID: 2, Metric: "ram", Severity: "warning", State: "firing", FiredAt: time.Now()},
	}

	rows := buildEventRows(events, names, countries)
	if len(rows) != 2 {
		t.Fatalf("buildEventRows() len = %d, want 2", len(rows))
	}

	// Server with a known country: flag and hover title present.
	if rows[0].FlagEmoji != "🇯🇵" {
		t.Errorf("row[0].FlagEmoji = %q, want Japan flag", rows[0].FlagEmoji)
	}
	if rows[0].CountryName != "Japan" {
		t.Errorf("row[0].CountryName = %q, want %q", rows[0].CountryName, "Japan")
	}

	// Server without a detected country: no flag, no title.
	if rows[1].FlagEmoji != "" {
		t.Errorf("row[1].FlagEmoji = %q, want empty", rows[1].FlagEmoji)
	}
	if rows[1].CountryName != "" {
		t.Errorf("row[1].CountryName = %q, want empty", rows[1].CountryName)
	}
}
