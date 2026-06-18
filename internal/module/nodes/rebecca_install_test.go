package nodes

import (
	"encoding/base64"
	"strings"
	"testing"
)

// testRebeccaCertPEM is a real self-signed certificate PEM used to exercise the
// install-input validation and command shaping. It is a certificate only — the
// docker dev install path reads just the client certificate from stdin.
const testRebeccaCertPEM = `-----BEGIN CERTIFICATE-----
MIIC/zCCAeegAwIBAgIUMkxG68SAZ1SZtjzy03DmVs9PbBwwDQYJKoZIhvcNAQEL
BQAwDzENMAsGA1UEAwwEdGVzdDAeFw0yNjA2MTgxMjAwMjlaFw0yNzA2MTgxMjAw
MjlaMA8xDTALBgNVBAMMBHRlc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEK
AoIBAQDZxjvQ/o0K59sDPf0PzZvv9smm7t0PtqmbXQnHJUnR9BAAwj/ZBwCbx+ME
F0FSN5WHn2V+yHvmjZ6sPagraUi9ApJNUc40cGJ1PVevlzNBRkM2dYYzJQpMviEL
A1iRzP+oGwf7zdSzmP7hCIJ7R6OPix88zsSmWv/H/xvge0YZ1SnTatIkAVT1zXi2
PeY71+DVeQXD6gJQvW2/YlkVgTq2piEFyxSzp2Cy+1kki9QbkbTI79q7uSGRLOqg
4HvVhTp+l5XovinoYuxORyYWM3vY6FUoYJT9+yTJczO/odmCPfw9+jwy0fwPXvmM
BImdjc8AiHICtJbk2nT+Av8aQLRTAgMBAAGjUzBRMB0GA1UdDgQWBBQRTL8tnOue
KkaR8QP6ELRH/AJXATAfBgNVHSMEGDAWgBQRTL8tnOueKkaR8QP6ELRH/AJXATAP
BgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQAMtmpnXP4waMhiU+Hu
04r0y+He4aX5iOte2J910OvmzRkVlkZ6+0DzWqOUOMP9b9MbHzNAupQBtlIF2Sp8
qhpNL0XDKpM5T+mWeIzPXwaFET/Hg57zk30mBVg8HOQgfXNNYhdHYER5WO4J9Bno
i9oQ4nwdpxIyAtqVwe5GchgBHtmUz98kTKuDsOQDDb173XilXySda7c2Hs0rZscD
yfloRaLo6Vw2Po4NC7+HKQz5x/i+Eg4ExmbErMhakDO2yukazPTtNbU5xzqAsiAe
rRSBlcNgMddiTpPihrWj/kxpBR3MS9tV6hOnnTVatIverAVYoKmJ7Fg9Uz7/TLNg
d3pD
-----END CERTIFICATE-----`

func TestRebeccaInstallChannels(t *testing.T) {
	channels := RebeccaProvider{}.InstallChannels()
	got := map[string]bool{}
	for _, c := range channels {
		got[c.Key] = c.Enabled
	}
	if enabled, ok := got[channelDev]; !ok || !enabled {
		t.Errorf("dev channel = (%v,%v), want enabled", enabled, ok)
	}
	if enabled, ok := got[channelStable]; !ok || enabled {
		t.Errorf("stable channel = (%v,%v), want present but disabled (coming soon)", enabled, ok)
	}
	if !rebeccaChannelEnabled(channelDev) {
		t.Errorf("rebeccaChannelEnabled(dev) = false, want true")
	}
	if rebeccaChannelEnabled(channelStable) {
		t.Errorf("rebeccaChannelEnabled(stable) = true, want false (coming soon)")
	}
}

func TestRebeccaNormalizeInstallInputValid(t *testing.T) {
	cfg, errs := RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Certificate: testRebeccaCertPEM,
		Channel:     "dev",
	})
	if errs.HasAny() {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Ports default when omitted.
	if cfg.ServicePort != rebeccaDefaultServicePort {
		t.Errorf("ServicePort = %q, want default %s", cfg.ServicePort, rebeccaDefaultServicePort)
	}
	if cfg.APIPort != rebeccaDefaultAPIPort {
		t.Errorf("APIPort = %q, want default %s", cfg.APIPort, rebeccaDefaultAPIPort)
	}
	if cfg.Channel != channelDev {
		t.Errorf("Channel = %q, want dev", cfg.Channel)
	}

	cfg, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Certificate: testRebeccaCertPEM,
		ServicePort: "70", APIPort: "80",
	})
	if errs.HasAny() {
		t.Fatalf("unexpected errors for custom ports: %v", errs)
	}
	if cfg.ServicePort != "70" || cfg.APIPort != "80" {
		t.Errorf("custom ports = %q/%q, want 70/80", cfg.ServicePort, cfg.APIPort)
	}
}

func TestRebeccaNormalizeInstallInputErrors(t *testing.T) {
	// Out-of-range ports, equal ports, and a non-PEM certificate.
	_, errs := RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Certificate: "not a certificate",
		ServicePort: "0", APIPort: "99999",
	})
	for _, field := range []string{"service_port", "api_port", "certificate"} {
		if _, ok := errs[field]; !ok {
			t.Errorf("expected error for %q, got %v", field, errs)
		}
	}

	// Equal ports: api must differ from service.
	_, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Certificate: testRebeccaCertPEM,
		ServicePort: "62050", APIPort: "62050",
	})
	if _, ok := errs["api_port"]; !ok {
		t.Errorf("expected api_port error when ports equal, got %v", errs)
	}

	// Empty certificate is a distinct, required error.
	_, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{Channel: "dev"})
	if errs["certificate"] != "nodes.err_cert_required" {
		t.Errorf("empty cert error = %q, want nodes.err_cert_required", errs["certificate"])
	}

	// A disabled channel (stable) is rejected.
	_, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Certificate: testRebeccaCertPEM,
		Channel:     "stable",
	})
	if _, ok := errs["channel"]; !ok {
		t.Errorf("expected channel error for disabled stable channel, got %v", errs)
	}
}

func TestRebeccaInstallCommand(t *testing.T) {
	cfg := RebeccaInstallConfig{
		Channel:     "dev",
		ServicePort: "62050",
		APIPort:     "62051",
		Certificate: testRebeccaCertPEM,
	}
	command, err := RebeccaProvider{}.InstallCommand(cfg)
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}

	// The dev script URL and the --dev / --name flags must be present.
	for _, want := range []string{
		rebeccaDevScriptURL,
		"install --name rebecca-node --dev",
		"base64 -d",
		"timeout",
		`printf "\n62050\n62051\n"`,
	} {
		if !strings.Contains(command, want) {
			t.Errorf("install command missing %q:\n%s", want, command)
		}
	}

	// The certificate must travel as base64 — the raw PEM must NOT appear in the
	// command (it is sensitive and must not leak into logs/echoed output).
	if strings.Contains(command, "BEGIN CERTIFICATE") {
		t.Errorf("raw certificate leaked into the install command:\n%s", command)
	}
	wantB64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(testRebeccaCertPEM) + "\n"))
	if !strings.Contains(command, wantB64) {
		t.Errorf("install command does not carry the base64-encoded certificate")
	}
	// Decoding the embedded base64 must reproduce the certificate.
	decoded, derr := base64.StdEncoding.DecodeString(wantB64)
	if derr != nil || !strings.Contains(string(decoded), "BEGIN CERTIFICATE") {
		t.Errorf("embedded base64 does not decode back to the certificate")
	}

	// A malformed config must be rejected before any shell is produced.
	if _, err := (RebeccaProvider{}).InstallCommand(RebeccaInstallConfig{Certificate: "nope"}); err == nil {
		t.Fatalf("InstallCommand must reject a non-PEM certificate")
	}
	if _, err := (RebeccaProvider{}).InstallCommand(RebeccaInstallConfig{Certificate: testRebeccaCertPEM, ServicePort: "1", APIPort: "1"}); err == nil {
		t.Fatalf("InstallCommand must reject equal service/API ports")
	}
}

func TestRebeccaBuildInstallPlan(t *testing.T) {
	plan, errs := RebeccaProvider{}.BuildInstallPlan(installFormInput{
		Certificate: testRebeccaCertPEM,
		Channel:     "dev",
	})
	if errs.HasAny() {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(plan.Steps) = %d, want 1 (no configure step)", len(plan.Steps))
	}
	if plan.Steps[0].TolerateTimeout {
		t.Errorf("Rebecca install step must not tolerate the remote timeout (it detaches)")
	}
	// No readback: the user supplied the certificate, nothing is read back.
	if plan.Readback.Command != "" {
		t.Errorf("Rebecca plan must have no readback command, got %q", plan.Readback.Command)
	}

	// Validation errors propagate as field-keyed i18n keys.
	_, errs = RebeccaProvider{}.BuildInstallPlan(installFormInput{Certificate: "bad"})
	if _, ok := errs["certificate"]; !ok {
		t.Errorf("expected certificate error from BuildInstallPlan, got %v", errs)
	}
}
