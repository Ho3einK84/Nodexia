package nodes

import (
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/view"
)

// The fleet header is pure server-side aggregation. These cases pin the
// behaviour that regressed: the counters must reflect the snapshots rather
// than staying at their zero value.
func TestBuildNodeFleetView(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []view.NodeSnapshotView
		want      view.NodeFleetView
	}{
		{
			name:      "no snapshots",
			snapshots: nil,
			want:      view.NodeFleetView{},
		},
		{
			name: "running and stopped nodes with ports",
			snapshots: []view.NodeSnapshotView{
				{
					HealthStatus: "running",
					ServicePort:  "62011",
					APIPort:      "-",
					ActivePorts:  []string{"62012", "-"},
					UptimeKnown:  true,
					UptimePct:    100,
				},
				{
					HealthStatus: "stopped",
					ServicePort:  "-",
					APIPort:      "62013",
					UptimePct:    50,
				},
			},
			want: view.NodeFleetView{
				Total:       2,
				Running:     1,
				Stopped:     1,
				Ports:       3,
				PortList:    []string{"62011", "62012", "62013"},
				HealthPct:   100,
				HealthKnown: true,
			},
		},
		{
			name: "padded and mixed-case statuses plus unknown",
			snapshots: []view.NodeSnapshotView{
				{HealthStatus: "  HEALTHY  "},
				{HealthStatus: "Active"},
				{HealthStatus: "Inactive"},
				{HealthStatus: "not_detected"},
				{HealthStatus: "-"},
			},
			want: view.NodeFleetView{
				Total:   5,
				Running: 2,
				Stopped: 1,
				Unknown: 2,
			},
		},
		{
			name: "duplicate ports collapse and uptime averages",
			snapshots: []view.NodeSnapshotView{
				{HealthStatus: "running", ServicePort: "443", APIPort: "443", ActivePorts: []string{"443"}, UptimeKnown: true, UptimePct: 99},
				{HealthStatus: "running", ServicePort: "443", UptimeKnown: true, UptimePct: 100},
			},
			want: view.NodeFleetView{
				Total:       2,
				Running:     2,
				Ports:       1,
				PortList:    []string{"443"},
				HealthPct:   99,
				HealthKnown: true,
			},
		},
		{
			name: "uptime-only snapshots without ports",
			snapshots: []view.NodeSnapshotView{
				{HealthStatus: "running"},
			},
			want: view.NodeFleetView{
				Total:   1,
				Running: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNodeFleetView(tt.snapshots)
			if got.Total != tt.want.Total ||
				got.Running != tt.want.Running ||
				got.Stopped != tt.want.Stopped ||
				got.Unknown != tt.want.Unknown ||
				got.Ports != tt.want.Ports ||
				got.HealthPct != tt.want.HealthPct ||
				got.HealthKnown != tt.want.HealthKnown {
				t.Fatalf("buildNodeFleetView() = %+v, want %+v", got, tt.want)
			}
			if len(got.PortList) != len(tt.want.PortList) {
				t.Fatalf("PortList = %v, want %v", got.PortList, tt.want.PortList)
			}
			for i := range tt.want.PortList {
				if got.PortList[i] != tt.want.PortList[i] {
					t.Fatalf("PortList = %v, want %v", got.PortList, tt.want.PortList)
				}
			}
		})
	}
}
