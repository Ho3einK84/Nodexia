package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

// Try --json first (vnstat 2.x). If it fails or is absent, print a sentinel and
// fall back to plain-text output so older vnstat builds still work.
const trafficCollectCommand = `sh -c 'command -v vnstat >/dev/null 2>&1 || { printf "__nodexia_vnstat_missing__\n"; exit 0; }; vnstat --json 2>/dev/null && exit 0; printf "__nodexia_vnstat_text__\n"; vnstat 2>/dev/null'`

func CollectTraffic(ctx context.Context, sshService *sshclient.Service, req sshclient.CommandRequest, preferredInterface string) (TrafficSnapshot, sshclient.CommandResult, error) {
	result, err := sshService.RunCommand(ctx, sshclient.CommandRequest{
		ConnectionRequest: req.ConnectionRequest,
		Command:           trafficCollectCommand,
		CommandTimeout:    req.CommandTimeout,
	})
	if err != nil {
		return TrafficSnapshot{}, result, err
	}

	output := strings.TrimSpace(result.Stdout)
	if output == "__nodexia_vnstat_missing__" {
		return TrafficSnapshot{
			Available:   false,
			Message:     "vnStat is not installed on the target server.",
			CollectedAt: result.CompletedAt,
		}, result, nil
	}

	if result.ExitCode != nil && *result.ExitCode != 0 {
		return TrafficSnapshot{
			Available:   false,
			Message:     fallbackDisplay(strings.TrimSpace(result.Stderr + "\n" + result.Stdout)),
			CollectedAt: result.CompletedAt,
		}, result, nil
	}

	var interfaces []vnstatInterface

	if strings.HasPrefix(output, "__nodexia_vnstat_text__") {
		text := strings.TrimLeft(strings.TrimPrefix(output, "__nodexia_vnstat_text__"), "\r\n")
		interfaces = parseVnstatText(text)
	} else {
		var payload vnstatPayload
		if jsonErr := json.Unmarshal([]byte(output), &payload); jsonErr != nil {
			// JSON parse failed; try text as last resort (e.g. partial JSON upgrade).
			interfaces = parseVnstatText(output)
			if len(interfaces) == 0 {
				return TrafficSnapshot{}, result, fmt.Errorf("monitoring: parse vnstat output: %w", jsonErr)
			}
		} else {
			interfaces = payload.Interfaces
		}
	}

	ifaceNames := make([]string, 0, len(interfaces))
	for _, item := range interfaces {
		if name := strings.TrimSpace(item.Name); name != "" {
			ifaceNames = append(ifaceNames, name)
		}
	}
	sort.Strings(ifaceNames)

	selected, message := selectTrafficInterface(interfaces, preferredInterface)
	if selected == nil {
		return TrafficSnapshot{
			Available:           false,
			AvailableInterfaces: ifaceNames,
			Message:             message,
			CollectedAt:         result.CompletedAt,
		}, result, nil
	}

	return TrafficSnapshot{
		Available:           true,
		InterfaceName:       selected.Name,
		AvailableInterfaces: ifaceNames,
		DailyRows:           tailTrafficRows(buildDailyRows(selected.Traffic.Days), 7),
		MonthlyRows:         tailTrafficRows(buildMonthlyRows(selected.Traffic.Months), 6),
		PeakMbps:            computePeakMbps(selected.Traffic.Hours),
		AvgMbps:             computeAvgMbps(selected.Traffic.Hours),
		Message:             "vnStat data loaded successfully.",
		CollectedAt:         result.CompletedAt,
	}, result, nil
}

// ── vnstat JSON structs ───────────────────────────────────────────────────────

type vnstatPayload struct {
	Interfaces []vnstatInterface `json:"interfaces"`
}

type vnstatInterface struct {
	Name    string        `json:"name"`
	Traffic vnstatTraffic `json:"traffic"`
}

type vnstatTraffic struct {
	Days   []vnstatDay   `json:"day"`   // vnstat 2.x JSON uses singular key
	Months []vnstatMonth `json:"month"` // vnstat 2.x JSON uses singular key
	Hours  []vnstatHour  `json:"hour"`  // hourly data for bandwidth rate calculation
}

type vnstatDay struct {
	Date vnstatDayDate `json:"date"`
	RX   int64         `json:"rx"`
	TX   int64         `json:"tx"`
}

type vnstatMonth struct {
	Date vnstatMonthDate `json:"date"`
	RX   int64           `json:"rx"`
	TX   int64           `json:"tx"`
}

type vnstatDayDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

type vnstatMonthDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
}

type vnstatHourTime struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
}

type vnstatHour struct {
	Date vnstatDayDate  `json:"date"`
	Time vnstatHourTime `json:"time"`
	RX   int64          `json:"rx"`
	TX   int64          `json:"tx"`
}

// ── Interface selection ───────────────────────────────────────────────────────

// selectTrafficInterface returns the preferred interface when it has traffic.
// If the preferred interface has zero accumulated bytes (e.g. docker0 with no
// traffic), or no preference is given, it falls back to the interface with the
// highest total byte count so that active physical NICs are chosen automatically.
func selectTrafficInterface(items []vnstatInterface, preferred string) (*vnstatInterface, string) {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		for index := range items {
			if strings.EqualFold(strings.TrimSpace(items[index].Name), preferred) {
				if interfaceTotalBytes(items[index]) > 0 {
					return &items[index], ""
				}
				break // preferred found but empty; fall through to auto-select
			}
		}
	}

	// Auto-select: highest accumulated traffic wins.
	best := -1
	var bestBytes int64
	for index := range items {
		if strings.TrimSpace(items[index].Name) == "" {
			continue
		}
		if total := interfaceTotalBytes(items[index]); best == -1 || total > bestBytes {
			bestBytes = total
			best = index
		}
	}
	if best >= 0 {
		return &items[best], ""
	}
	return nil, "vnStat did not return any monitored interfaces yet."
}

func interfaceTotalBytes(iface vnstatInterface) int64 {
	var total int64
	for _, d := range iface.Traffic.Days {
		total += d.RX + d.TX
	}
	for _, m := range iface.Traffic.Months {
		total += m.RX + m.TX
	}
	return total
}

// ── Row builders ──────────────────────────────────────────────────────────────

func buildDailyRows(items []vnstatDay) []TrafficRow {
	rows := make([]TrafficRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, TrafficRow{
			Label:      fmt.Sprintf("%04d-%02d-%02d", item.Date.Year, item.Date.Month, item.Date.Day),
			RXBytes:    item.RX,
			TXBytes:    item.TX,
			TotalBytes: item.RX + item.TX,
		})
	}
	return rows
}

func buildMonthlyRows(items []vnstatMonth) []TrafficRow {
	rows := make([]TrafficRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, TrafficRow{
			Label:      fmt.Sprintf("%04d-%02d", item.Date.Year, item.Date.Month),
			RXBytes:    item.RX,
			TXBytes:    item.TX,
			TotalBytes: item.RX + item.TX,
		})
	}
	return rows
}

func tailTrafficRows(rows []TrafficRow, limit int) []TrafficRow {
	if len(rows) <= limit {
		return rows
	}
	return rows[len(rows)-limit:]
}

// ── Bandwidth rate computation ────────────────────────────────────────────

// computePeakMbps returns the maximum observed throughput across all available
// hourly entries. vnstat stores rx+tx per hour; dividing by 3600 seconds and
// multiplying by 8 gives the average rate over that hour in Mbps.
func computePeakMbps(hours []vnstatHour) float64 {
	var peak float64
	for _, h := range hours {
		mbps := float64(h.RX+h.TX) * 8.0 / 3600.0 / 1_000_000.0
		if mbps > peak {
			peak = mbps
		}
	}
	return peak
}

// computeAvgMbps returns the mean hourly throughput rate across all available
// hourly entries. Empty hours (zero traffic) are included so the result
// represents a true time-averaged rate, not just active-period average.
func computeAvgMbps(hours []vnstatHour) float64 {
	if len(hours) == 0 {
		return 0
	}
	var total float64
	for _, h := range hours {
		total += float64(h.RX+h.TX) * 8.0 / 3600.0 / 1_000_000.0
	}
	return total / float64(len(hours))
}

// ── Plain-text vnstat parser ──────────────────────────────────────────────────
//
// Handles two output formats:
//
// Format A — vnstat 2.x compact (multiple interfaces, "/" separator):
//
//	docker0:
//	      2026-06    0 B  /  0 B  /  0 B  /  --
//	    yesterday    0 B  /  0 B  /  0 B
//	        today    1.48 GiB  /  1.57 GiB  /  3.05 GiB  /  62.78 GiB
//
// Format B — vnstat 1.x table (single interface, "|" separator):
//
//	eth0 since 2026-05-29
//	  monthly
//	    2026-05  920.07 GiB |  916.53 GiB |  1.79 TiB  |  5.89 Mbit/s
//	  daily
//	    yesterday  608.63 GiB |  604.41 GiB |  1.18 TiB  |  120.60 Mbit/s

func parseVnstatText(output string) []vnstatInterface {
	var interfaces []vnstatInterface
	var current *vnstatInterface

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Interface header has highest priority — check before anything else.
		if name := extractInterfaceName(trimmed); name != "" {
			if current != nil {
				interfaces = append(interfaces, *current)
			}
			current = &vnstatInterface{Name: name}
			continue
		}

		if isSeparatorLine(trimmed) {
			continue
		}

		lower := strings.ToLower(trimmed)
		if lower == "monthly" || lower == "daily" || lower == "yearly" || lower == "hourly" {
			continue
		}

		if current == nil {
			continue
		}

		label, rx, tx, ok := parseTrafficDataLine(trimmed)
		if !ok {
			continue
		}

		switch {
		case isMonthLabel(label):
			year, month := parseYearMonth(label)
			if year > 0 {
				current.Traffic.Months = append(current.Traffic.Months, vnstatMonth{
					Date: vnstatMonthDate{Year: year, Month: month},
					RX:   rx,
					TX:   tx,
				})
			}
		case label == "today" || label == "yesterday":
			year, month, day := resolveDayLabel(label)
			if year > 0 {
				current.Traffic.Days = append(current.Traffic.Days, vnstatDay{
					Date: vnstatDayDate{Year: year, Month: month, Day: day},
					RX:   rx,
					TX:   tx,
				})
			}
		case isFullDateLabel(label):
			year, month, day := parseFullDate(label)
			if year > 0 {
				current.Traffic.Days = append(current.Traffic.Days, vnstatDay{
					Date: vnstatDayDate{Year: year, Month: month, Day: day},
					RX:   rx,
					TX:   tx,
				})
			}
		}
	}

	if current != nil {
		interfaces = append(interfaces, *current)
	}
	return interfaces
}

func isSeparatorLine(trimmed string) bool {
	if strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "===") {
		return true
	}
	if strings.HasPrefix(trimmed, "Database") {
		return true
	}
	lower := strings.ToLower(trimmed)
	// "rx  | tx | total | avg. rate" header row and "rx: X TiB ..." summary line.
	if strings.HasPrefix(lower, "rx ") || strings.HasPrefix(lower, "rx\t") || lower == "rx" || strings.HasPrefix(lower, "rx:") {
		return true
	}
	if strings.HasPrefix(lower, "estimated") {
		return true
	}
	return false
}

// extractInterfaceName recognises two patterns:
//   - Format A: "docker0:"  (name immediately followed by colon, no spaces)
//   - Format B: "eth0 since 2026-05-29"
func extractInterfaceName(trimmed string) string {
	if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
		name := trimmed[:len(trimmed)-1]
		if isValidInterfaceName(name) {
			return name
		}
	}
	parts := strings.Fields(trimmed)
	if len(parts) >= 3 && strings.ToLower(parts[1]) == "since" && isValidInterfaceName(parts[0]) {
		return parts[0]
	}
	return ""
}

func isValidInterfaceName(name string) bool {
	if name == "" || len(name) > 16 {
		return false
	}
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '_' || ch == '-' || ch == '.' || ch == ':' || ch == '@' {
			continue
		}
		return false
	}
	return true
}

// parseTrafficDataLine extracts (label, rx, tx) from a line delimited by "|" or "/".
// Columns: "LABEL  RX_VALUE UNIT / TX_VALUE UNIT / TOTAL / ..."
func parseTrafficDataLine(trimmed string) (label string, rx int64, tx int64, ok bool) {
	sep := ""
	if strings.Contains(trimmed, "|") {
		sep = "|"
	} else if strings.Contains(trimmed, "/") {
		sep = "/"
	}
	if sep == "" {
		return "", 0, 0, false
	}

	parts := strings.SplitN(trimmed, sep, 4)
	if len(parts) < 2 {
		return "", 0, 0, false
	}

	// First segment contains the row label and the RX value.
	firstFields := strings.Fields(parts[0])
	if len(firstFields) < 3 {
		return "", 0, 0, false
	}
	label = firstFields[0]
	if label == "estimated" || label == "rx" || label == "tx" || label == "total" {
		return "", 0, 0, false
	}

	rxStr := firstFields[len(firstFields)-2] + " " + firstFields[len(firstFields)-1]
	rx = parseHumanBytes(rxStr)

	txFields := strings.Fields(parts[1])
	if len(txFields) < 2 {
		return "", 0, 0, false
	}
	tx = parseHumanBytes(txFields[0] + " " + txFields[1])

	return label, rx, tx, true
}

func parseHumanBytes(s string) int64 {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) < 2 {
		return 0
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 {
		return 0
	}
	var mult float64
	switch strings.ToUpper(fields[1]) {
	case "B":
		mult = 1
	case "KIB", "KB", "K":
		mult = 1024
	case "MIB", "MB", "M":
		mult = 1024 * 1024
	case "GIB", "GB", "G":
		mult = 1024 * 1024 * 1024
	case "TIB", "TB", "T":
		mult = 1024 * 1024 * 1024 * 1024
	case "PIB", "PB", "P":
		mult = 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return 0
	}
	return int64(value * mult)
}

func isMonthLabel(label string) bool {
	if len(label) != 7 {
		return false
	}
	p := strings.SplitN(label, "-", 2)
	if len(p) != 2 || len(p[0]) != 4 || len(p[1]) != 2 {
		return false
	}
	_, e1 := strconv.Atoi(p[0])
	_, e2 := strconv.Atoi(p[1])
	return e1 == nil && e2 == nil
}

func parseYearMonth(label string) (year, month int) {
	p := strings.SplitN(label, "-", 2)
	if len(p) != 2 {
		return 0, 0
	}
	year, _ = strconv.Atoi(p[0])
	month, _ = strconv.Atoi(p[1])
	return
}

func isFullDateLabel(label string) bool {
	if len(label) != 10 {
		return false
	}
	p := strings.SplitN(label, "-", 3)
	if len(p) != 3 {
		return false
	}
	_, e1 := strconv.Atoi(p[0])
	_, e2 := strconv.Atoi(p[1])
	_, e3 := strconv.Atoi(p[2])
	return e1 == nil && e2 == nil && e3 == nil
}

func parseFullDate(label string) (year, month, day int) {
	p := strings.SplitN(label, "-", 3)
	if len(p) != 3 {
		return 0, 0, 0
	}
	year, _ = strconv.Atoi(p[0])
	month, _ = strconv.Atoi(p[1])
	day, _ = strconv.Atoi(p[2])
	return
}

func resolveDayLabel(label string) (year, month, day int) {
	now := time.Now().UTC()
	var t time.Time
	switch label {
	case "today":
		t = now
	case "yesterday":
		t = now.AddDate(0, 0, -1)
	default:
		return 0, 0, 0
	}
	return t.Year(), int(t.Month()), t.Day()
}
