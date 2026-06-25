package nodes

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ssNP is `ss -tlnp` output for a host running two xray nodes plus system
// services. The first node owns pids 1234 (xray) and 1200 (its agent, on the
// management ports 62033/62034); a SECOND node's xray is pid 9999; sshd is pid
// 700. Only the first node's xray inbound (5505) must be attributed to it.
const ssNP = `LISTEN 0 4096 *:5505 *:* users:(("xray",pid=1234,fd=7))
LISTEN 0 4096 *:8443 *:* users:(("xray",pid=1234,fd=9),("xray",pid=1234,fd=10))
LISTEN 0 4096 *:62033 *:* users:(("rebecca-node",pid=1200,fd=3))
LISTEN 0 4096 *:62034 *:* users:(("rebecca-node",pid=1200,fd=4))
LISTEN 0 4096 *:880 *:* users:(("xray",pid=9999,fd=7))
LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=700,fd=3))
LISTEN 0 4096 127.0.0.1:9000 0.0.0.0:* users:(("agent",pid=1234,fd=9))`

func TestParseListenSockets(t *testing.T) {
	got := parseListenSockets(strings.Split(ssNP, "\n"))
	// Loopback (127.0.0.1:9000) is dropped; the rest keep their PIDs.
	want := []listenSocket{
		{Port: "5505", PIDs: []string{"1234"}},
		{Port: "8443", PIDs: []string{"1234", "1234"}},
		{Port: "62033", PIDs: []string{"1200"}},
		{Port: "62034", PIDs: []string{"1200"}},
		{Port: "880", PIDs: []string{"9999"}},
		{Port: "22", PIDs: []string{"700"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseListenSockets() = %#v, want %#v", got, want)
	}
}

func TestXrayPortsForPIDs(t *testing.T) {
	sockets := parseListenSockets(strings.Split(ssNP, "\n"))
	// Node owns pids 1234 (xray) + 1200 (agent); exclude its mgmt ports.
	got := xrayPortsForPIDs(sockets, []string{"1234", "1200"}, "62033", "62034")
	want := []string{"5505", "8443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xrayPortsForPIDs() = %v, want %v (must exclude other node's 880, sshd 22, mgmt 62033/62034)", got, want)
	}

	// No PIDs → no ports (never fall back to the whole host).
	if got := xrayPortsForPIDs(sockets, nil); got != nil {
		t.Fatalf("xrayPortsForPIDs(nil pids) = %v, want nil", got)
	}
}

func TestRebeccaDiscoveryAttributesXrayPortsByPID(t *testing.T) {
	output := "=REBECCANODE=rebecca-node=\n" +
		"=ENVSTART=\nSERVICE_PORT=62033\nXRAY_API_PORT=62034\n=ENVEND=\n" +
		"=RELEASESTART=\n=RELEASEEND=\n=MODE=binary=\n=SERVICE=active=\n" +
		"=PIDS=1234 1200 =\n=REBECCANODEEND=\n" +
		"=LISTENP=\n" + ssNP + "\n=LISTENPEND=\n"
	snaps := RebeccaProvider{}.ParseDiscovery(output, time.Now())
	if len(snaps) != 1 {
		t.Fatalf("len(snaps) = %d, want 1", len(snaps))
	}
	want := []string{"5505", "8443"}
	if !reflect.DeepEqual(snaps[0].XrayPorts, want) {
		t.Fatalf("XrayPorts = %v, want %v", snaps[0].XrayPorts, want)
	}
}

// TestDiscoveryCommandsShellSyntax guards both discovery commands (including the
// listening-port/PID probes) against shell-syntax breakage and stray single
// quotes that would break the sh -c '…' wrapper.
func TestDiscoveryCommandsShellSyntax(t *testing.T) {
	for _, p := range DefaultProviders() {
		cmd := p.DiscoveryCommand()
		if cmd == "" {
			continue
		}
		if strings.Count(cmd, "'") != 2 {
			t.Errorf("%s: discovery command should have exactly 2 single quotes (the wrapper)", p.Type())
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(cmd, "sh -c '"), "'")
		c := exec.Command("sh", "-n")
		c.Stdin = strings.NewReader(inner)
		if out, err := c.CombinedOutput(); err != nil {
			t.Errorf("%s: sh -n failed: %v\n%s", p.Type(), err, out)
		}
	}
}
