package nodes

import (
	"encoding/base64"
	"strings"
	"testing"
)

// testRebeccaCertPEM / testRebeccaKeyPEM are a real self-signed certificate and
// matching RSA private key. The binary dev install reads a BUNDLE — the
// certificate followed by its private key — from stdin, so the tests exercise
// both blocks. The key is PKCS#1 ("RSA PRIVATE KEY") because the installer's
// terminator requires a key type with a prefix word.
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

const testRebeccaKeyPEM = `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA6gqumWkn7jXpfkGgc43YoOnbA2f51ppNWEDGebAMfdmSTC7O
7f7+1TxTsQ/yVx0uhWziWm9MrAPae6r5418tm7or9/p7VgRV8QbV8pKVuLVPbmG+
ci75+xJjuZ6pwpLb417t0pn9JEnj2tnsoCZamm78ihKX/CFdEEWoS7C9rvjUSP0v
qQyEmvBXVdY0EzvjZOSLGbwCVPKOEnBsbRsZ8tThF7kIR8FjDoRhpm2IYrEOjXUt
OsGMCxWZF4U9Um8hPrAzWFNoP4E1LlE7C8t7xLSeHFZqla/k+idw8iVctWjcBfF6
3kuRjT3Cg+lNfFnBUZ0xqK4Md9sPBtqoi4LS7QIDAQABAoIBAAtRIuo2JIEnSDgb
skeEJ2J4jGeYwoL3CSBoWXCO67u2JpXaeZUWjHoBJcbdD4nY1mQLRNK8qQd0VD9A
oD63Xnw2P2QJT6d0JDe4beYB4o2A7utWfKAG132lgP77xherhEh1UaiqW4xCqmrt
uLxxvlXTYhDHH2RItLhRtfabAEESnX0HlrHXnufthpb5iRu0cm+qKdPegTlCPFEO
6BOFNbTiXTkX5ANP++DZXKnDF1Gy8aoJmvJowoM33ygt+auds72DDNyd2cgJVuql
kujPjpRcsMlRuz2YGZcH0inyU189VuUQZC3t7+qOKBuIOMuE8hBhV6fsezHWzdP6
RKPIrF0CgYEA9lpPtv9njGPu/9SKDwIbgAUY+WgAcetgLLnfrXUKGQVUTTyl1lsb
vsslyORWXTqjf8g5NU16UKcluEiUAkbd/A6aSqZuuehHWAEZr2xPJe+mhTC/fIpw
GKiFq8FixOW/YK/X5Xjm2Us876AzTzuTasyhBuX+PL7MxS8+b8N8Mv8CgYEA8zTz
xkKKVOixKec8JKTAkZKjyMkNTv9i6hZOQER2sMio4SCzTCmOrxn9SwkJly4Cdg1u
ntKQGqzaR2XqDZj8uAiE38FD5DtYpVtijr4Qii+XLGNZKq9z41TUfGyUmhuzpkey
kO1XUoNFCBHPp9Ypp0aWiiSqOlR7SoGRq5G29hMCgYEAu6nod7rwIp4t/mzmDrDI
SimX8MYtMJrhVLDzl6tE2fKZWY0Nt9EHvbv7OKHYuIRm8HySN+yhdLcfoNaJCYL6
r3xgROWsC6rKTlvoOR4E3R1GeMe91x2ObvpReZmDqAJsWzcY/BGxqW4LKW+cJot3
rS/cquihV5zxWHS412LPRfkCgYAUt+sYdayxJQ2Ko09FU9+vxw062p3OoAT+Kh5K
bUqrLrzsSMvdbiDgm9cvIDr37Qx6oBRPZWKvUxBZSr5QoDrPNrKTGTS+aavYklto
C5r/GqTHPENpVn8J270qSFm0cy2vuaXloMJyngowcMv+4Ui1HldOt2blBzNlmnod
YpFyjwKBgBl2bSlvzrPOCb0jV8EwBnA+odscDlklXcj8KQfslyO8B/O6YH6dyZWO
XKl/kct3ixwWQBaHyhkj18NtndQf4mF8Jd7c7duYRDq2m1WMR6rhSbgeXe43dGys
PaJ10LF6KLwYJKtVm44caQ6pn3M3b9KBz4TMdW44gnFMsRk0GFj0
-----END RSA PRIVATE KEY-----`

// testRebeccaBundle is what the user pastes: certificate then private key.
var testRebeccaBundle = testRebeccaCertPEM + "\n" + testRebeccaKeyPEM

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

func TestNormalizeRebeccaBundle(t *testing.T) {
	// Certificate-then-key passes through in order.
	out, ok := normalizeRebeccaBundle(testRebeccaBundle)
	if !ok {
		t.Fatalf("normalizeRebeccaBundle(cert+key) ok = false, want true")
	}
	if strings.Index(out, "BEGIN CERTIFICATE") > strings.Index(out, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("normalized bundle must place the certificate before the key")
	}

	// Key-then-cert is re-ordered to cert-then-key (the order the script needs).
	reversed := testRebeccaKeyPEM + "\n" + testRebeccaCertPEM
	out, ok = normalizeRebeccaBundle(reversed)
	if !ok {
		t.Fatalf("normalizeRebeccaBundle(key+cert) ok = false, want true")
	}
	if strings.Index(out, "BEGIN CERTIFICATE") > strings.Index(out, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("normalized bundle must re-order to certificate-before-key")
	}

	// Missing the key, or missing the cert, is rejected.
	if _, ok := normalizeRebeccaBundle(testRebeccaCertPEM); ok {
		t.Errorf("a certificate without a private key must be rejected")
	}
	if _, ok := normalizeRebeccaBundle(testRebeccaKeyPEM); ok {
		t.Errorf("a private key without a certificate must be rejected")
	}
	// A bare PKCS#8 key (no prefix word) is rejected — the installer can't read it.
	pkcs8 := testRebeccaCertPEM + "\n-----BEGIN PRIVATE KEY-----\nMIIBVgIBADAN\n-----END PRIVATE KEY-----"
	if _, ok := normalizeRebeccaBundle(pkcs8); ok {
		t.Errorf("a bare PKCS#8 PRIVATE KEY must be rejected (installer needs a prefixed key type)")
	}
}

func TestRebeccaNormalizeInstallInputValid(t *testing.T) {
	cfg, errs := RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Bundle:  testRebeccaBundle,
		Channel: "dev",
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
	// The normalized bundle carries both blocks, cert first.
	if !strings.Contains(cfg.Bundle, "BEGIN CERTIFICATE") || !strings.Contains(cfg.Bundle, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("normalized bundle missing a block: %q", cfg.Bundle)
	}

	cfg, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Bundle:      testRebeccaBundle,
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
	// Out-of-range ports and a non-PEM bundle.
	_, errs := RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Bundle:      "not a bundle",
		ServicePort: "0", APIPort: "99999",
	})
	for _, field := range []string{"service_port", "api_port", "bundle"} {
		if _, ok := errs[field]; !ok {
			t.Errorf("expected error for %q, got %v", field, errs)
		}
	}

	// Equal ports: api must differ from service.
	_, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Bundle:      testRebeccaBundle,
		ServicePort: "62050", APIPort: "62050",
	})
	if _, ok := errs["api_port"]; !ok {
		t.Errorf("expected api_port error when ports equal, got %v", errs)
	}

	// Empty bundle is a distinct, required error.
	_, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{Channel: "dev"})
	if errs["bundle"] != "nodes.err_bundle_required" {
		t.Errorf("empty bundle error = %q, want nodes.err_bundle_required", errs["bundle"])
	}

	// A certificate without a private key is rejected as a bad bundle.
	_, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{Bundle: testRebeccaCertPEM})
	if errs["bundle"] != "nodes.err_bundle_pem" {
		t.Errorf("cert-only bundle error = %q, want nodes.err_bundle_pem", errs["bundle"])
	}

	// A disabled channel (stable) is rejected.
	_, errs = RebeccaProvider{}.normalizeInstallInput(installFormInput{
		Bundle:  testRebeccaBundle,
		Channel: "stable",
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
		Bundle:      testRebeccaBundle,
	}
	command, err := RebeccaProvider{}.InstallCommand(cfg)
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}

	// The dev script URL and the binary/dev/name flags must be present, and the
	// flavor must be forced to binary.
	for _, want := range []string{
		rebeccaDevScriptURL,
		"REBECCA_NODE_SCRIPT_FLAVOR=binary",
		"install --name rebecca-node --binary --dev",
		"base64 -d",
		"timeout",
		`printf "62050\n62051\n"`,
	} {
		if !strings.Contains(command, want) {
			t.Errorf("install command missing %q:\n%s", want, command)
		}
	}

	// The bundle (cert + private key) must travel as base64 — neither the raw
	// certificate nor the private key may appear in the command (sensitive).
	if strings.Contains(command, "BEGIN CERTIFICATE") || strings.Contains(command, "PRIVATE KEY") {
		t.Errorf("raw certificate/key leaked into the install command:\n%s", command)
	}
	// Decoding the embedded base64 must reproduce the bundle (cert + key).
	normalized, _ := cfg.Normalize()
	wantB64 := base64.StdEncoding.EncodeToString([]byte(normalized.Bundle))
	if !strings.Contains(command, wantB64) {
		t.Errorf("install command does not carry the base64-encoded bundle")
	}
	decoded, derr := base64.StdEncoding.DecodeString(wantB64)
	if derr != nil || !strings.Contains(string(decoded), "BEGIN CERTIFICATE") || !strings.Contains(string(decoded), "PRIVATE KEY") {
		t.Errorf("embedded base64 does not decode back to the cert+key bundle")
	}

	// A malformed config must be rejected before any shell is produced.
	if _, err := (RebeccaProvider{}).InstallCommand(RebeccaInstallConfig{Bundle: "nope"}); err == nil {
		t.Fatalf("InstallCommand must reject a non-PEM bundle")
	}
	if _, err := (RebeccaProvider{}).InstallCommand(RebeccaInstallConfig{Bundle: testRebeccaBundle, ServicePort: "1", APIPort: "1"}); err == nil {
		t.Fatalf("InstallCommand must reject equal service/API ports")
	}
}

func TestRebeccaBuildInstallPlan(t *testing.T) {
	plan, errs := RebeccaProvider{}.BuildInstallPlan(installFormInput{
		Bundle:  testRebeccaBundle,
		Channel: "dev",
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
	// No readback: the user supplied the bundle, nothing is read back.
	if plan.Readback.Command != "" {
		t.Errorf("Rebecca plan must have no readback command, got %q", plan.Readback.Command)
	}

	// Validation errors propagate as field-keyed i18n keys.
	_, errs = RebeccaProvider{}.BuildInstallPlan(installFormInput{Bundle: "bad"})
	if _, ok := errs["bundle"]; !ok {
		t.Errorf("expected bundle error from BuildInstallPlan, got %v", errs)
	}
}
