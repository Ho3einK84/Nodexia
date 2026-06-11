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
