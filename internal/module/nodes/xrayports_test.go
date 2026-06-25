package nodes

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ssSample is real `ss -tln` output from a Rebecca binary node (the node's own
// ports are 62033/62034/62035; the rest of the public listeners are xray
// inbounds). Loopback and DNS listeners must be dropped.
const ssSample = `State   Recv-Q   Send-Q     Local Address:Port      Peer Address:Port  Process
LISTEN  0        4096           127.0.0.1:14878          0.0.0.0:*
LISTEN  0        4096           127.0.0.1:53234          0.0.0.0:*
LISTEN  0        4096       127.0.0.53%lo:53             0.0.0.0:*
LISTEN  0        4096          127.0.0.54:53             0.0.0.0:*
LISTEN  0        4096             0.0.0.0:22             0.0.0.0:*
LISTEN  0        4096                   *:5505                 *:*
LISTEN  0        4096                   *:62033                *:*
LISTEN  0        4096                   *:62034                *:*
LISTEN  0        4096                   *:8443                 *:*
LISTEN  0        4096                   *:2053                 *:*
LISTEN  0        4096                [::]:22                [::]:*`

func TestParseListenPorts(t *testing.T) {
	got := parseListenPorts(strings.Split(ssSample, "\n"))
	want := []string{"22", "2053", "5505", "8443", "62033", "62034"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseListenPorts() = %v, want %v", got, want)
	}
}

func TestXrayPortsFromListen(t *testing.T) {
	listen := parseListenPorts(strings.Split(ssSample, "\n"))
	// Exclude the node's service (62033) and api (62034) ports; 22 (SSH) and 53
	// (DNS) are dropped as well-known non-xray.
	got := xrayPortsFromListen(listen, "62033", "62034")
	want := []string{"2053", "5505", "8443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xrayPortsFromListen() = %v, want %v", got, want)
	}
}

func TestRebeccaDiscoverySurfacesXrayPorts(t *testing.T) {
	output := "=REBECCANODE=rebecca-node=\n=ENVSTART=\nSERVICE_PORT=62033\nXRAY_API_PORT=62034\n=ENVEND=\n=RELEASESTART=\n=RELEASEEND=\n=MODE=binary=\n=SERVICE=active=\n=REBECCANODEEND=\n" +
		"=LISTEN=\n" + ssSample + "\n=LISTENEND=\n"
	snaps := RebeccaProvider{}.ParseDiscovery(output, time.Now())
	if len(snaps) != 1 {
		t.Fatalf("len(snaps) = %d, want 1", len(snaps))
	}
	want := []string{"2053", "5505", "8443"}
	if !reflect.DeepEqual(snaps[0].XrayPorts, want) {
		t.Fatalf("XrayPorts = %v, want %v", snaps[0].XrayPorts, want)
	}
}

// TestDiscoveryCommandsShellSyntax guards the listening-port probe (and the rest
// of each discovery command) against shell-syntax breakage and stray single
// quotes that would break the sh -c '…' wrapper.
func TestDiscoveryCommandsShellSyntax(t *testing.T) {
	for _, p := range DefaultProviders() {
		cmd := p.DiscoveryCommand()
		if cmd == "" {
			continue
		}
		if strings.Count(cmd, "'") != 2 {
			t.Errorf("%s: discovery command should have exactly 2 single quotes (the wrapper): %s", p.Type(), cmd)
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(cmd, "sh -c '"), "'")
		c := exec.Command("sh", "-n")
		c.Stdin = strings.NewReader(inner)
		if out, err := c.CombinedOutput(); err != nil {
			t.Errorf("%s: sh -n failed: %v\n%s", p.Type(), err, out)
		}
	}
}
