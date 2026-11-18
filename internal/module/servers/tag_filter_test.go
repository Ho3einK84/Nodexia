package servers

import "testing"

func TestFilterByTag(t *testing.T) {
	list := []Server{
		{ID: 1, Name: "a", Tags: []string{"prod", "eu"}},
		{ID: 2, Name: "b", Tags: []string{"dev"}},
		{ID: 3, Name: "c", Tags: []string{"prod"}},
		{ID: 4, Name: "d"},
	}

	if got := filterByTag(list, ""); len(got) != 4 {
		t.Fatalf("empty tag filtered to %d, want all 4", len(got))
	}
	got := filterByTag(list, "prod")
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("prod filter = %#v, want servers 1 and 3", got)
	}
	if got := filterByTag(list, "missing"); len(got) != 0 {
		t.Fatalf("missing tag matched %d servers, want 0", len(got))
	}
}

func TestBuildTagOptions(t *testing.T) {
	list := []Server{
		{ID: 1, Tags: []string{"prod", "eu"}},
		{ID: 2, Tags: []string{"prod"}},
	}

	opts := buildTagOptions(list, "web", "prod")
	if len(opts) != 2 {
		t.Fatalf("got %d options, want 2 (eu, prod)", len(opts))
	}
	// Alphabetical: eu first.
	if opts[0].Tag != "eu" || opts[0].Active || opts[0].Count != 1 {
		t.Fatalf("eu chip = %+v", opts[0])
	}
	// The active chip links back WITHOUT its tag (toggle off) but keeps the query.
	if !opts[1].Active || opts[1].Count != 2 {
		t.Fatalf("prod chip = %+v", opts[1])
	}
	if opts[1].URL != "/servers?q=web" {
		t.Fatalf("active chip URL = %q, want /servers?q=web", opts[1].URL)
	}
	// An inactive chip carries both the query and its tag.
	if opts[0].URL != "/servers?q=web&tag=eu" {
		t.Fatalf("inactive chip URL = %q, want /servers?q=web&tag=eu", opts[0].URL)
	}
}
