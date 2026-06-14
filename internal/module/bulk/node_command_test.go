package bulk

import (
	"os/exec"
	"strings"
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

func TestNodeRestartCommandContent(t *testing.T) {
	cmd := nodeRestartCommand()
	// PasarGuard: discovered exactly like the discovery probe, driven via pg-node.
	for _, want := range []string{
		`for dir in /opt/*/`,
		`grep -Eqi "pasarguard|pg-node"`,
		`command -v pg-node`,
		`--name "$name" restart -n </dev/null`,
		// Rebecca: detected by dir or CLI, restarted via its CLI.
		`/opt/rebecca-node`,
		`$SUDO rebecca-node restart </dev/null`,
		// Sudo preamble (88) + non-zero rollup.
		`exit 88`,
		`exit $fail`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("node-restart command missing %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "update") {
		t.Errorf("node-restart command must not run an update:\n%s", cmd)
	}
}

func TestNodeUpdateCommandContent(t *testing.T) {
	cmd := nodeUpdateCommand()
	for _, want := range []string{
		`--name "$name" update --yes </dev/null`,
		`yes | $SUDO rebecca-node update`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("node-update command missing %q:\n%s", want, cmd)
		}
	}
}

// TestNodeCommandsShellSyntax pipes each generated node command through `sh -n`
// to catch quoting / control-flow mistakes (mirrors the nodes module guard).
func TestNodeCommandsShellSyntax(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available on PATH")
	}
	for name, cmd := range map[string]string{
		"node-restart": nodeRestartCommand(),
		"node-update":  nodeUpdateCommand(),
	} {
		out, err := exec.Command("sh", "-n", "-c", cmd).CombinedOutput()
		if err != nil {
			t.Errorf("%s: sh -n failed: %v\n%s\ncommand:\n%s", name, err, out, cmd)
		}
	}
}
