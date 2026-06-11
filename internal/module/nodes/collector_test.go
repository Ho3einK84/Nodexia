package nodes

import "testing"

func TestParseServices(t *testing.T) {
	output := "rebecca-node.service loaded active running Rebecca node\n"

	services := parseServices(output)
	if len(services) != 1 {
		t.Fatalf("len(services) = %d, want 1", len(services))
	}
	if services[0].Name != "rebecca-node.service" {
		t.Fatalf("Name = %q", services[0].Name)
	}
	if services[0].ActiveState != "active" {
		t.Fatalf("ActiveState = %q", services[0].ActiveState)
	}
}

func TestParseListeners(t *testing.T) {
	output := "LISTEN 0 128 0.0.0.0:62050 0.0.0.0:* users:((\"rebecca-node\",pid=123,fd=3))\n"

	listeners := parseListeners(output)
	if len(listeners) != 1 {
		t.Fatalf("len(listeners) = %d, want 1", len(listeners))
	}
	if listeners[0].Port != 62050 {
		t.Fatalf("Port = %d, want 62050", listeners[0].Port)
	}
}
