package nodes

import (
	"sort"
	"strconv"
	"strings"
)

// Xray inbound-port detection.
//
// A node's xray ports are the public TCP ports its OWN xray process listens on
// — not every port open on the host. Attributing ports to a specific node
// matters because one host can run several nodes (plus SSH, DNS, web servers…),
// and a host-wide port list would wrongly show all of them on every node.
//
// We attribute by process ownership: discovery captures `ss -tlnp` (listening
// sockets WITH their owning PIDs) once per host, and each node block carries the
// set of PIDs that belong to it — systemd cgroup members for binary installs,
// `docker top` for containers. A socket counts as the node's xray port only when
// its PID is one of the node's, minus the node's own management (service/api)
// ports. PasarGuard additionally unions the published container ports so bridge
// networking (where the listener is docker-proxy, not the container) still works.

// listenProbeCommand captures the host's listening TCP sockets with owning
// process info, wrapped in =LISTENP=…=LISTENPEND=. `-H` drops the header; the
// plain form is the fallback. `-p` needs root/sudo to reveal other processes,
// which discovery already runs with.
const listenProbeCommand = `printf "=LISTENP=\n"; ` +
	`($SUDO ss -tlnpH 2>/dev/null || $SUDO ss -tlnp 2>/dev/null || true); ` +
	`printf "=LISTENPEND=\n"; `

// listenSocket is one listening TCP socket: its port and the PIDs that own it.
type listenSocket struct {
	Port string
	PIDs []string
}

// parseListenSockets parses `ss -tlnp` output into public listening sockets.
// Loopback-only listeners (127.0.0.0/8, ::1) and the header row are dropped.
func parseListenSockets(lines []string) []listenSocket {
	var out []listenSocket
	for _, raw := range lines {
		fields := strings.Fields(raw)
		if len(fields) < 4 {
			continue
		}
		// `State Recv-Q Send-Q Local:Port Peer:Port users:((…))` — field[3] is the
		// local address; a trailing "%iface"/zone is handled by LastIndex(":").
		addr := fields[3]
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			continue
		}
		host, port := addr[:idx], addr[idx+1:]
		if !isAllDigits(port) || port == "0" {
			continue // header row or malformed
		}
		if isLoopbackHost(host) {
			continue
		}
		out = append(out, listenSocket{Port: port, PIDs: extractPIDs(raw)})
	}
	return out
}

// extractPIDs pulls every pid=N from an `ss -tlnp` process column, e.g.
// users:(("xray",pid=1234,fd=7),("xray",pid=1234,fd=8)).
func extractPIDs(line string) []string {
	var pids []string
	for {
		i := strings.Index(line, "pid=")
		if i < 0 {
			break
		}
		line = line[i+len("pid="):]
		j := 0
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j > 0 {
			pids = append(pids, line[:j])
		}
		line = line[j:]
	}
	return pids
}

// xrayPortsForPIDs returns the ports whose owning PID is one of the node's,
// excluding its management ports (service/api). Result is unique and numerically
// sorted. An empty PID set yields no ports (so a node we cannot attribute simply
// shows none rather than the whole host).
func xrayPortsForPIDs(sockets []listenSocket, nodePIDs []string, exclude ...string) []string {
	if len(nodePIDs) == 0 {
		return nil
	}
	pidSet := make(map[string]struct{}, len(nodePIDs))
	for _, p := range nodePIDs {
		if p != "" {
			pidSet[p] = struct{}{}
		}
	}
	skip := map[string]struct{}{}
	for _, e := range exclude {
		if e = strings.TrimSpace(e); e != "" {
			skip[e] = struct{}{}
		}
	}

	seen := map[string]struct{}{}
	var out []string
	for _, s := range sockets {
		if _, bad := skip[s.Port]; bad {
			continue
		}
		if _, dup := seen[s.Port]; dup {
			continue
		}
		if !ownedBy(s.PIDs, pidSet) {
			continue
		}
		seen[s.Port] = struct{}{}
		out = append(out, s.Port)
	}
	sortPortsNumeric(out)
	return out
}

func ownedBy(pids []string, set map[string]struct{}) bool {
	for _, p := range pids {
		if _, ok := set[p]; ok {
			return true
		}
	}
	return false
}

// parsePIDs extracts the numeric PIDs from a whitespace-separated list (cgroup
// procs or `docker top` output), ignoring any header/non-numeric tokens.
func parsePIDs(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if isAllDigits(f) {
			out = append(out, f)
		}
	}
	return out
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if i := strings.Index(host, "%"); i >= 0 { // strip zone id (e.g. "::1%lo")
		host = host[:i]
	}
	return strings.HasPrefix(host, "127.") || host == "::1"
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func sortPortsNumeric(ports []string) {
	sort.Slice(ports, func(i, j int) bool {
		a, _ := strconv.Atoi(ports[i])
		b, _ := strconv.Atoi(ports[j])
		return a < b
	})
}
