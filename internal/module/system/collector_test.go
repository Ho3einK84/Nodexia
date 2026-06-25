package system

import "testing"

func TestParseCollectorOutput(t *testing.T) {
	output := "hostname=vps-1\nos_name=Ubuntu\nos_version=24.04\nkernel_version=6.8.0\narchitecture=x86_64\nuptime_seconds=3600\nlast_update_unix=1710000000\n"

	values, err := parseCollectorOutput(output)
	if err != nil {
		t.Fatalf("parseCollectorOutput() error = %v", err)
	}

	if values["hostname"] != "vps-1" {
		t.Fatalf("hostname = %q", values["hostname"])
	}
	if values["os_version"] != "24.04" {
		t.Fatalf("os_version = %q", values["os_version"])
	}
}

func TestParseCollectorOutputRejectsMalformedLine(t *testing.T) {
	_, err := parseCollectorOutput("hostname=vps-1\nbroken-line\n")
	if err == nil {
		t.Fatal("expected malformed line error")
	}
}

func TestParseInt64(t *testing.T) {
	if got := parseInt64("42"); got != 42 {
		t.Fatalf("parseInt64() = %d, want 42", got)
	}
	if got := parseInt64(""); got != 0 {
		t.Fatalf("parseInt64(empty) = %d, want 0", got)
	}
}

func TestFormatKiB(t *testing.T) {
	cases := map[int64]string{
		0:         "-",
		-5:        "-",
		16461176:  "16 GiB",   // ~15.7 → rounded GiB for >=10
		2097152:   "2.0 GiB",  // exactly 2 GiB
		264212084: "252 GiB",  // root disk
		1610612736: "1.5 TiB", // 1.5 TiB in KiB
	}
	for in, want := range cases {
		if got := formatKiB(in); got != want {
			t.Errorf("formatKiB(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatCount(t *testing.T) {
	if got := formatCount(0); got != "-" {
		t.Errorf("formatCount(0) = %q, want -", got)
	}
	if got := formatCount(4); got != "4" {
		t.Errorf("formatCount(4) = %q, want 4", got)
	}
}
