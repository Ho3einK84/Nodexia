// Package livemetrics streams real-time server resource metrics to connected
// dashboard clients over WebSocket.
//
// It is deliberately independent of the scheduled snapshot pipeline
// (internal/module/monitoring): the snapshot collector keeps running on its
// timer and persisting to the database untouched, while this package adds an
// opt-in, ephemeral live view.
//
// # Design
//
//   - One Hub holds at most one *broker per server.
//   - A broker owns a SINGLE long-lived SSH connection that runs a remote loop
//     emitting one metrics frame every interval. However many dashboard clients
//     watch the same server, they share that one connection — no per-client
//     polling, no extra load on the target.
//   - Each client gets a Subscription with a latest-wins buffered channel, so a
//     slow client never blocks collection or other clients.
//   - When the last subscriber leaves, the broker is stopped after a short grace
//     period (so a quick reconnect reuses the warm connection), which closes the
//     SSH connection and tears down the remote loop.
//
// Live metrics are never persisted; the broker caches only the most recent
// frame in memory so a newly connecting client renders instantly.
package livemetrics

import "time"

// DiskUsage is one mounted filesystem's usage at collection time.
type DiskUsage struct {
	Mount   string  `json:"mount"`
	Percent float64 `json:"percent"`
	UsedKB  int64   `json:"usedKB"`
	TotalKB int64   `json:"totalKB"`
}

// Metrics is one real-time sample of a server's resource usage. It is the JSON
// payload pushed to the browser inside a {"type":"metrics","data":<Metrics>}
// frame.
type Metrics struct {
	CPUPercent    float64     `json:"cpuPercent"`
	PerCore       []float64   `json:"perCore"`
	MemTotalKB    int64       `json:"memTotalKB"`
	MemUsedKB     int64       `json:"memUsedKB"`
	MemFreeKB     int64       `json:"memFreeKB"`
	MemAvailKB    int64       `json:"memAvailKB"`
	MemPercent    float64     `json:"memPercent"`
	SwapPercent   float64     `json:"swapPercent"`
	Disks         []DiskUsage `json:"disks"`
	Load1         float64     `json:"load1"`
	Load5         float64     `json:"load5"`
	Load15        float64     `json:"load15"`
	UptimeSeconds int64       `json:"uptimeSeconds"`
	NetRxBytes    int64       `json:"netRxBytes"`
	NetTxBytes    int64       `json:"netTxBytes"`
	CollectedAt   time.Time   `json:"collectedAt"`
}

// Update is one fan-out item delivered to a subscriber: either a fresh Metrics
// sample or a transient error message (e.g. the SSH connection dropped). The
// client renders Error as a "reconnecting" notice without tearing down the UI.
type Update struct {
	Metrics *Metrics `json:"-"`
	Error   string   `json:"-"`
}
