package notify

import (
	"strings"
	"testing"
)

func sampleMessage() AlertMessage {
	return AlertMessage{
		Server:    "edge-1",
		Metric:    "CPU usage",
		Value:     "93%",
		Threshold: "≥ 90%",
		Severity:  "warning",
		FiredAt:   "2026-06-11 12:00:00 UTC",
	}
}

func TestRenderMessageDefault(t *testing.T) {
	out, err := RenderMessage("", sampleMessage())
	if err != nil {
		t.Fatalf("RenderMessage() error = %v", err)
	}
	for _, want := range []string{"edge-1", "CPU usage", "93%", "≥ 90%", "WARNING"} {
		if !strings.Contains(out, want) {
			t.Fatalf("default message missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMessageOverride(t *testing.T) {
	out, err := RenderMessage("{{ .Server }}: {{ .Metric }} hit {{ .Value }}", sampleMessage())
	if err != nil {
		t.Fatalf("RenderMessage() error = %v", err)
	}
	if out != "edge-1: CPU usage hit 93%" {
		t.Fatalf("override message = %q", out)
	}
}

func TestRenderMessageOverrideCanUseFuncs(t *testing.T) {
	out, err := RenderMessage("{{ upper .Severity }} {{ icon .Severity }}", sampleMessage())
	if err != nil {
		t.Fatalf("RenderMessage() error = %v", err)
	}
	if !strings.HasPrefix(out, "WARNING ") {
		t.Fatalf("expected upper severity prefix, got %q", out)
	}
}

func TestRenderMessageInvalidTemplate(t *testing.T) {
	if _, err := RenderMessage("{{ .Server", sampleMessage()); err == nil {
		t.Fatal("expected parse error for malformed template")
	}
}
