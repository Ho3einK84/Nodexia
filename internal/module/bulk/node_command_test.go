package bulk

import (
	"testing"
)

func TestHumanizeBulkAction(t *testing.T) {
	cases := map[string]string{
		"node-restart": "node restart",
		"node-update":  "node update",
		"update":       "package update",
		"reboot":       "reboot",
		"delete":       "delete",
	}
	for action, want := range cases {
		if got := humanizeBulkAction(action); got != want {
			t.Errorf("humanizeBulkAction(%q) = %q, want %q", action, got, want)
		}
	}
}

// TestNodeActionKeys pins the bulk→nodes action-key mapping: node-restart and
// node-update must drive the canonical "restart"/"update" per-node actions.
func TestNodeActionKeys(t *testing.T) {
	cases := map[string]string{
		"node-restart": "restart",
		"node-update":  "update",
	}
	for bulkAction, want := range cases {
		if got := nodeActionKeys[bulkAction]; got != want {
			t.Errorf("nodeActionKeys[%q] = %q, want %q", bulkAction, got, want)
		}
	}
}
