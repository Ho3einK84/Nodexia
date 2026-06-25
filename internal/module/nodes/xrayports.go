package nodes

import (
	"sort"
	"strconv"
	"strings"
)

// listenProbeCommand is appended to a provider's discovery command to capture
// the host's listening TCP ports in one pass. Xray inbounds (binary and
// host-networked nodes) listen here, so the parser can surface them as xray
// ports. `ss` is preferred; netstat is the portable fallback. The output is
// wrapped in =LISTEN=…=LISTENEND= so ParseDiscovery can isolate it.
const listenProbeCommand = `printf "=LISTEN=\n"; ` +
	`($SUDO ss -tlnH 2>/dev/null || $SUDO ss -tln 2>/dev/null || $SUDO netstat -tln 2>/dev/null || true); ` +
	`printf "=LISTENEND=\n"; `

// wellKnownNonXrayPorts are public listeners that are never xray inbounds, so
// they are excluded from the detected xray ports (SSH and DNS).
var wellKnownNonXrayPorts = map[string]struct{}{
	"22": {}, // SSH
	"53": {}, // DNS
}

// parseListenPorts extracts the publicly reachable TCP listening ports from
// `ss -tln` / `netstat -tln` output. Loopback-only listeners (127.0.0.0/8, ::1)
// are dropped since they are node-internal, as is the header row. Ports are
// returned unique and numerically sorted.
func parseListenPorts(lines []string) []string {
	seen := map[string]struct{}{}
	var ports []string
	for _, raw := range lines {
		fields := strings.Fields(raw)
		if len(fields) < 4 {
			continue
		}
		// `State Recv-Q Send-Q Local:Port Peer:Port …` — field[3] is the local
		// address. ss may also prefix an interface ("%lo"); LastIndex(":") still
		// isolates the port.
		addr := fields[3]
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			continue
		}
		host, port := addr[:idx], addr[idx+1:]
		if !isAllDigits(port) || port == "" || port == "0" {
			continue // header row or malformed
		}
		if isLoopbackHost(host) {
			continue
		}
		if _, ok := seen[port]; ok {
			continue
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	sortPortsNumeric(ports)
	return ports
}

// xrayPortsFromListen returns the listening ports that look like xray inbounds:
// every public listener minus the well-known system ports and the node's own
// management ports (service/api), which are surfaced separately.
func xrayPortsFromListen(listen []string, exclude ...string) []string {
	if len(listen) == 0 {
		return nil
	}
	skip := map[string]struct{}{}
	for k := range wellKnownNonXrayPorts {
		skip[k] = struct{}{}
	}
	for _, e := range exclude {
		if e = strings.TrimSpace(e); e != "" {
			skip[e] = struct{}{}
		}
	}
	var out []string
	for _, p := range listen {
		if _, ok := skip[p]; ok {
			continue
		}
		out = append(out, p)
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
