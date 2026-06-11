package servers

import "testing"

func mkServers(n int) []Server {
	out := make([]Server, n)
	for i := range out {
		out[i] = Server{ID: int64(i + 1), Name: "srv", Host: "h"}
	}
	return out
}

func TestPaginateServersClamp(t *testing.T) {
	items, cur, total := paginateServers(mkServers(23), 99, 10)
	if total != 3 || cur != 3 || len(items) != 3 {
		t.Fatalf("got cur=%d total=%d len=%d", cur, total, len(items))
	}
	items, cur, total = paginateServers(mkServers(0), 1, 10)
	if total != 1 || cur != 1 || len(items) != 0 {
		t.Fatalf("empty: cur=%d total=%d len=%d", cur, total, len(items))
	}
	items, cur, _ = paginateServers(mkServers(23), 0, 10)
	if cur != 1 || len(items) != 10 {
		t.Fatalf("page0: cur=%d len=%d", cur, len(items))
	}
}

func TestFilterServers(t *testing.T) {
	servers := []Server{
		{Name: "DE(Hetzner)", Host: "5.75.199.2", Port: 22, Tags: []string{"prod"}},
		{Name: "US-East", Host: "1.2.3.4", Port: 2222, Tags: []string{"staging"}},
	}
	if got := filterServers(servers, "hetz"); len(got) != 1 || got[0].Name != "DE(Hetzner)" {
		t.Fatalf("name match failed: %+v", got)
	}
	if got := filterServers(servers, "staging"); len(got) != 1 || got[0].Name != "US-East" {
		t.Fatalf("tag match failed: %+v", got)
	}
	if got := filterServers(servers, "2222"); len(got) != 1 {
		t.Fatalf("port match failed: %+v", got)
	}
	if got := filterServers(servers, ""); len(got) != 2 {
		t.Fatalf("empty query should pass all: %+v", got)
	}
}

func TestPageWindow(t *testing.T) {
	if got := pageWindow(1, 5); len(got) != 5 {
		t.Fatalf("<=7 should list all: %v", got)
	}
	// 10 pages, current 5 -> [1,0,4,5,6,0,10]
	got := pageWindow(5, 10)
	want := []int{1, 0, 4, 5, 6, 0, 10}
	if len(got) != len(want) {
		t.Fatalf("window len: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("window: got %v want %v", got, want)
		}
	}
}
