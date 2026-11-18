package livemetrics

import (
	"strconv"
	"strings"
	"time"
)

// frameDelimiter terminates one metrics frame in the remote stream. The remote
// loop prints a block of key=value lines and then this delimiter on its own
// line; the parser flushes a Metrics on seeing it.
const frameDelimiter = "=ENDFRAME="

// collectCommand builds the remote shell loop that emits one metrics frame per
// interval over a single SSH session.
//
// It reads /proc and df directly (no agent install) and emits, per frame:
//
//	cpu=<overall %>
//	core=<cpuN> <%>            (one line per core)
//	mem=<totalKB> <usedKB> <freeKB> <availKB> <pct>
//	swap=<pct>
//	disk=<mount> <pct> <usedKB> <totalKB>   (one line per real mount, capped)
//	load=<1m> <5m> <15m>
//	uptime=<seconds>
//	net=<rxBytes> <txBytes>
//	ts=<unix seconds>
//	=ENDFRAME=
//
// CPU is sampled across a 1s window (two /proc/stat reads); the remaining
// interval is slept at the tail. awk programs are double-quoted with $ and "
// escaped so the whole script stays free of single quotes (it is wrapped in
// sh -c '...'). This mirrors the snapshot collector's quoting approach.
func collectCommand(interval time.Duration) string {
	tail := int(interval.Seconds()) - 1
	if tail < 1 {
		tail = 1
	}

	return `sh -c 'while :; do
S1="$(grep "^cpu" /proc/stat)"
sleep 1
S2="$(grep "^cpu" /proc/stat)"
printf "%s\n=SPLIT=\n%s\n" "$S1" "$S2" | awk "
  /^=SPLIT=\$/ { p=1; next }
  p==0 { pi[\$1]=\$5+\$6; pt[\$1]=\$2+\$3+\$4+\$5+\$6+\$7+\$8+\$9+\$10; next }
  {
    idle=\$5+\$6; tot=\$2+\$3+\$4+\$5+\$6+\$7+\$8+\$9+\$10
    di=idle-pi[\$1]; dt=tot-pt[\$1]; u=0
    if (dt>0) u=(1-di/dt)*100
    if (u<0) u=0; if (u>100) u=100
    if (\$1==\"cpu\") printf \"cpu=%.2f\n\", u; else printf \"core=%s %.2f\n\", \$1, u
  }
"
awk "/^MemTotal:/{t=\$2}/^MemFree:/{f=\$2}/^MemAvailable:/{a=\$2}END{u=t-a; if(a==0)u=t-f; p=0; if(t>0)p=u/t*100; printf \"mem=%d %d %d %d %.2f\n\", t, u, f, a, p}" /proc/meminfo
awk "/^SwapTotal:/{t=\$2}/^SwapFree:/{f=\$2}END{p=0; if(t>0)p=(t-f)/t*100; printf \"swap=%.2f\n\", p}" /proc/meminfo
df -P -k 2>/dev/null | awk "NR>1 && \$1!~/^(tmpfs|devtmpfs|squashfs|overlay|none|udev)\$/ && \$6!~/^\/(proc|sys|dev|run|snap)/ { gsub(/%/,\"\",\$5); printf \"disk=%s %s %s %s\n\", \$6, \$5, \$3, \$2 }" | head -n 12
read l1 l5 l15 _ < /proc/loadavg; printf "load=%s %s %s\n" "$l1" "$l5" "$l15"
printf "uptime=%s\n" "$(cut -d. -f1 /proc/uptime 2>/dev/null || echo 0)"
awk -F: "NR>2 { ifc=\$1; gsub(/ /,\"\",ifc); if (ifc!=\"lo\" && ifc!=\"\") { v=\$2; sub(/^[[:space:]]+/,\"\",v); split(v,d,/[[:space:]]+/); rx+=d[1]; tx+=d[9] } } END { printf \"net=%.0f %.0f\n\", rx, tx }" /proc/net/dev
printf "ts=%s\n" "$(date +%s 2>/dev/null || echo 0)"
printf "=ENDFRAME=\n"
sleep ` + strconv.Itoa(tail) + `
done'`
}

// frameParser accumulates the key=value lines of one frame and produces a
// *Metrics when it sees the frame delimiter. A single parser is reused across
// frames on one stream (it resets after each flush).
type frameParser struct {
	cur     Metrics
	started bool
}

// line feeds one stdout line. It returns a completed *Metrics (and true) when
// the line is the frame delimiter, otherwise (nil, false).
func (p *frameParser) line(raw string) (*Metrics, bool) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return nil, false
	}
	if line == frameDelimiter {
		if !p.started {
			return nil, false
		}
		done := p.cur
		if done.CollectedAt.IsZero() {
			done.CollectedAt = time.Now().UTC()
		}
		p.cur = Metrics{}
		p.started = false
		return &done, true
	}

	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return nil, false
	}
	p.started = true
	fields := strings.Fields(value)

	switch key {
	case "cpu":
		p.cur.CPUPercent = atof(value)
	case "core":
		if len(fields) == 2 {
			p.cur.PerCore = append(p.cur.PerCore, atof(fields[1]))
		}
	case "mem":
		if len(fields) == 5 {
			p.cur.MemTotalKB = atoi(fields[0])
			p.cur.MemUsedKB = atoi(fields[1])
			p.cur.MemFreeKB = atoi(fields[2])
			p.cur.MemAvailKB = atoi(fields[3])
			p.cur.MemPercent = atof(fields[4])
		}
	case "swap":
		p.cur.SwapPercent = atof(value)
	case "disk":
		if len(fields) == 4 {
			p.cur.Disks = append(p.cur.Disks, DiskUsage{
				Mount:   fields[0],
				Percent: atof(fields[1]),
				UsedKB:  atoi(fields[2]),
				TotalKB: atoi(fields[3]),
			})
		}
	case "load":
		if len(fields) == 3 {
			p.cur.Load1 = atof(fields[0])
			p.cur.Load5 = atof(fields[1])
			p.cur.Load15 = atof(fields[2])
		}
	case "uptime":
		p.cur.UptimeSeconds = atoi(value)
	case "net":
		if len(fields) == 2 {
			p.cur.NetRxBytes = atoi(fields[0])
			p.cur.NetTxBytes = atoi(fields[1])
		}
	case "ts":
		if secs := atoi(value); secs > 0 {
			p.cur.CollectedAt = time.Unix(secs, 0).UTC()
		}
	}
	return nil, false
}

func atof(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return parsed
}

func atoi(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}
