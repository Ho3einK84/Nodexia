package system

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

const collectCommand = `sh -lc 'set -eu
if [ -r /etc/os-release ]; then
  . /etc/os-release
fi
printf "hostname=%s\n" "$(hostname 2>/dev/null || uname -n)"
printf "os_name=%s\n" "${NAME:-${PRETTY_NAME:-unknown}}"
printf "os_version=%s\n" "${VERSION_ID:-${PRETTY_NAME:-unknown}}"
printf "kernel_version=%s\n" "$(uname -r)"
printf "architecture=%s\n" "$(uname -m)"
printf "cpu_model=%s\n" "$(awk -F": " "/^model name/{print \$2; exit}" /proc/cpuinfo 2>/dev/null || true)"
printf "cpu_cores=%s\n" "$(nproc 2>/dev/null || grep -c "^processor" /proc/cpuinfo 2>/dev/null || echo 0)"
printf "mem_total_kb=%s\n" "$(awk "/^MemTotal:/{print \$2; exit}" /proc/meminfo 2>/dev/null || echo 0)"
printf "disk_total_kb=%s\n" "$(df -Pk / 2>/dev/null | awk "NR==2{print \$2}" || echo 0)"
printf "uptime_seconds=%s\n" "$(cut -d. -f1 /proc/uptime 2>/dev/null || echo 0)"
last_update_unix=""
for candidate in /var/lib/apt/periodic/update-success-stamp /var/log/apt/history.log /var/cache/apt/pkgcache.bin; do
  if [ -e "$candidate" ]; then
    last_update_unix="$(stat -c %Y "$candidate" 2>/dev/null || true)"
    if [ -n "$last_update_unix" ]; then
      break
    fi
  fi
done
printf "last_update_unix=%s\n" "$last_update_unix"'`

func Collect(ctx context.Context, sshService *sshclient.Service, req sshclient.CommandRequest) (FactSnapshot, sshclient.CommandResult, error) {
	result, err := sshService.RunCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: req.ConnectionRequest,
		Command:           collectCommand,
		CommandTimeout:    req.CommandTimeout,
	})
	if err != nil {
		return FactSnapshot{}, result, err
	}

	values, parseErr := parseCollectorOutput(result.Stdout)
	if parseErr != nil {
		return FactSnapshot{}, result, parseErr
	}

	snapshot := FactSnapshot{
		Hostname:       values["hostname"],
		OSName:         values["os_name"],
		OSVersion:      values["os_version"],
		KernelVersion:  values["kernel_version"],
		Architecture:   values["architecture"],
		CPUModel:       values["cpu_model"],
		CPUCores:       parseInt64(values["cpu_cores"]),
		MemTotalKB:     parseInt64(values["mem_total_kb"]),
		DiskTotalKB:    parseInt64(values["disk_total_kb"]),
		UptimeSeconds:  parseInt64(values["uptime_seconds"]),
		LastUpdateUnix: parseInt64(values["last_update_unix"]),
		CollectedAt:    result.CompletedAt,
	}

	if snapshot.Hostname == "" && snapshot.OSName == "" && snapshot.KernelVersion == "" {
		return FactSnapshot{}, result, fmt.Errorf("system: collector returned incomplete output")
	}

	return snapshot, result, nil
}

func parseCollectorOutput(output string) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("system: malformed collector output line %q", line)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	return values, nil
}

func parseInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

// formatKiB renders a size given in kibibytes (1024-byte units, as /proc/meminfo
// and `df -k` report) as a human-readable GiB/TiB string. Zero/negative → "-".
func formatKiB(kib int64) string {
	if kib <= 0 {
		return "-"
	}
	const mib = 1024.0
	gib := float64(kib) / mib / mib
	if gib >= 1024 {
		return fmt.Sprintf("%.1f TiB", gib/1024)
	}
	if gib >= 10 {
		return fmt.Sprintf("%.0f GiB", gib)
	}
	return fmt.Sprintf("%.1f GiB", gib)
}

func formatUnixTimestamp(value int64) string {
	if value <= 0 {
		return "-"
	}
	return time.Unix(value, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "-"
	}

	days := seconds / 86400
	seconds = seconds % 86400
	hours := seconds / 3600
	seconds = seconds % 3600
	minutes := seconds / 60

	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 || len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	parts = append(parts, fmt.Sprintf("%dm", minutes))
	return strings.Join(parts, " ")
}
