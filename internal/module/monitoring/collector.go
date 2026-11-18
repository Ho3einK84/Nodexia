package monitoring

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

const collectCommand = `sh -c 'set -eu
cpu_before="$(grep "^cpu " /proc/stat)"
sleep 1
cpu_after="$(grep "^cpu " /proc/stat)"
cpu_usage="$(printf "%s\n%s\n" "$cpu_before" "$cpu_after" | awk "
  NR==1 {u1=\$2+\$3+\$4+\$7+\$8; t1=u1+\$5+\$6; next}
  NR==2 {u2=\$2+\$3+\$4+\$7+\$8; t2=u2+\$5+\$6; dt=t2-t1; du=u2-u1; if (dt > 0) printf \"%.2f\", (du/dt)*100; else printf \"0.00\"}
")"
ram_usage="$(awk "
  /^MemTotal:/ {total=\$2}
  /^MemAvailable:/ {available=\$2}
  END {if (total > 0) printf \"%.2f\", ((total-available)/total)*100; else printf \"0.00\"}
" /proc/meminfo)"
swap_usage="$(awk "
  /^SwapTotal:/ {total=\$2}
  /^SwapFree:/ {free=\$2}
  END {if (total > 0) printf \"%.2f\", ((total-free)/total)*100; else printf \"0.00\"}
" /proc/meminfo)"
disk_usage="$(df -P / | awk "NR==2 {gsub(/%/, \"\", \$5); print \$5}")"
read load1 load5 load15 _ < /proc/loadavg
uptime_seconds="$(cut -d. -f1 /proc/uptime 2>/dev/null || echo 0)"
network_summary="$(awk -F: "
  BEGIN {rx=0; tx=0; count=0}
  NR>2 {
    gsub(/ /, \"\", \$1)
    if (\$1 != \"lo\" && \$1 != \"\") {
      split(\$2, data, /[[:space:]]+/)
      rx += data[1]
      tx += data[9]
      count++
    }
  }
  END {printf \"interfaces=%d rx_bytes=%.0f tx_bytes=%.0f\", count, rx, tx}
" /proc/net/dev)"
printf "cpu_usage=%s\n" "$cpu_usage"
printf "ram_usage=%s\n" "$ram_usage"
printf "swap_usage=%s\n" "$swap_usage"
printf "disk_usage=%s\n" "$disk_usage"
printf "load_average_1=%s\n" "$load1"
printf "load_average_5=%s\n" "$load5"
printf "load_average_15=%s\n" "$load15"
printf "uptime_seconds=%s\n" "$uptime_seconds"
printf "network_summary=%s\n" "$network_summary"'`

func Collect(ctx context.Context, sshService *sshclient.Service, req sshclient.CommandRequest) (Snapshot, sshclient.CommandResult, error) {
	result, err := sshService.RunCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: req.ConnectionRequest,
		Command:           collectCommand,
		CommandTimeout:    req.CommandTimeout,
	})
	if err != nil {
		return Snapshot{}, result, err
	}

	values, parseErr := parseCollectorOutput(result.Stdout)
	if parseErr != nil {
		return Snapshot{}, result, parseErr
	}

	snapshot := Snapshot{
		CPUUsage:       parseFloat(values["cpu_usage"]),
		RAMUsage:       parseFloat(values["ram_usage"]),
		SwapUsage:      parseFloat(values["swap_usage"]),
		DiskUsage:      parseFloat(values["disk_usage"]),
		LoadAverage1:   parseFloat(values["load_average_1"]),
		LoadAverage5:   parseFloat(values["load_average_5"]),
		LoadAverage15:  parseFloat(values["load_average_15"]),
		UptimeSeconds:  parseInt64(values["uptime_seconds"]),
		NetworkSummary: values["network_summary"],
		CreatedAt:      result.CompletedAt,
	}

	if snapshot.NetworkSummary == "" && snapshot.CPUUsage == 0 && snapshot.RAMUsage == 0 && snapshot.DiskUsage == 0 {
		return Snapshot{}, result, fmt.Errorf("monitoring: collector returned incomplete output")
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
			return nil, fmt.Errorf("monitoring: malformed collector output line %q", line)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values, nil
}

func parseFloat(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		slog.Debug("monitoring: parse float failed", slog.String("value", value))
		return 0
	}
	return parsed
}

func parseInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		slog.Debug("monitoring: parse int failed", slog.String("value", value))
		return 0
	}
	return parsed
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

func formatPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64) + "%"
}

func formatLoad(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}
