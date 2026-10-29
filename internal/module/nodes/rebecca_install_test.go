package nodes

import (
	"encoding/base64"
	"regexp"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// testRebeccaCertPEM / testRebeccaKeyPEM are a real self-signed certificate and
// matching RSA private key. The binary dev install reads a BUNDLE — the
// certificate followed by its private key — from stdin, so the tests exercise
// both blocks.
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
	if enabled, ok := got[channelStable]; !ok || !enabled {
		t.Errorf("stable channel = (%v,%v), want present and enabled", enabled, ok)
	}
	if !rebeccaChannelEnabled(channelDev) {
		t.Errorf("rebeccaChannelEnabled(dev) = false, want true")
	}
	if !rebeccaChannelEnabled(channelStable) {
		t.Errorf("rebeccaChannelEnabled(stable) = false, want true")
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
	// The upstream installer also accepts a bare PKCS#8 key (no prefix word).
	pkcs8 := testRebeccaCertPEM + "\n-----BEGIN PRIVATE KEY-----\nMIIBVgIBADAN\n-----END PRIVATE KEY-----"
	if _, ok := normalizeRebeccaBundle(pkcs8); !ok {
		t.Errorf("a bare PKCS#8 PRIVATE KEY must be accepted")
	}
}

func TestRebeccaNormalizeInstallInputValid(t *testing.T) {
	cfg, errs := RebeccaProvider{}.normalizeInstallInput(InstallFormInput{
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

	cfg, errs = RebeccaProvider{}.normalizeInstallInput(InstallFormInput{
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
	_, errs := RebeccaProvider{}.normalizeInstallInput(InstallFormInput{
		Bundle:      "not a bundle",
		ServicePort: "0", APIPort: "99999",
	})
	for _, field := range []string{"service_port", "api_port", "bundle"} {
		if _, ok := errs[field]; !ok {
			t.Errorf("expected error for %q, got %v", field, errs)
		}
	}

	// Equal ports: api must differ from service.
	_, errs = RebeccaProvider{}.normalizeInstallInput(InstallFormInput{
		Bundle:      testRebeccaBundle,
		ServicePort: "62050", APIPort: "62050",
	})
	if _, ok := errs["api_port"]; !ok {
		t.Errorf("expected api_port error when ports equal, got %v", errs)
	}

	// Empty bundle is a distinct, required error.
	_, errs = RebeccaProvider{}.normalizeInstallInput(InstallFormInput{Channel: "dev"})
	if errs["bundle"] != "nodes.err_bundle_required" {
		t.Errorf("empty bundle error = %q, want nodes.err_bundle_required", errs["bundle"])
	}

	// A certificate without a private key is rejected as a bad bundle.
	_, errs = RebeccaProvider{}.normalizeInstallInput(InstallFormInput{Bundle: testRebeccaCertPEM})
	if errs["bundle"] != "nodes.err_bundle_pem" {
		t.Errorf("cert-only bundle error = %q, want nodes.err_bundle_pem", errs["bundle"])
	}

	// An unknown channel is rejected.
	_, errs = RebeccaProvider{}.normalizeInstallInput(InstallFormInput{
		Bundle:  testRebeccaBundle,
		Channel: "beta",
	})
	if _, ok := errs["channel"]; !ok {
		t.Errorf("expected channel error for unknown channel, got %v", errs)
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
		rebeccaScriptURLDev,
		"REBECCA_NODE_SCRIPT_FLAVOR=binary",
		"install --name rebecca-node --binary --dev",
		"timeout",
		// Stdin provides 'y' so the upstream directory override prompt proceeds,
		// and falls through to EOF for port prompts to honor pre-written .env values.
		`printf "y\n" | `,
		// Neutralize upstream's broken </dev/tty redirection in non-interactive sessions
		`sed -i "s|</dev/tty||g; s|\\[ -r /dev/tty \\]|false|g" "$SCRIPT"`,
	} {
		if !strings.Contains(command, want) {
			t.Errorf("install command missing %q:\n%s", want, command)
		}
	}

	// Verify stable channel sets --version latest.
	cfgStable := RebeccaInstallConfig{
		Channel:     "stable",
		ServicePort: "62050",
		APIPort:     "62051",
		Bundle:      testRebeccaBundle,
	}
	cmdStable, err := RebeccaProvider{}.InstallCommand(cfgStable)
	if err != nil {
		t.Fatalf("InstallCommand(stable): %v", err)
	}
	if !strings.Contains(cmdStable, "install --name rebecca-node --binary --version latest") {
		t.Errorf("stable install command missing --version latest flag:\n%s", cmdStable)
	}

	// The cert, key, and .env are pre-written via base64 in the install command.
	// They MUST be present so configure_binary_node_env skips all interactive prompts.
	for _, want := range []string{
		"base64",
		"/var/lib/rebecca-node/cert.pem",
		"/var/lib/rebecca-node/cert.key",
		"/opt/rebecca-node/.env",
	} {
		if !strings.Contains(command, want) {
			t.Errorf("install command missing marker %q:\n%s", want, command)
		}
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
	plan, errs := RebeccaProvider{}.BuildInstallPlan(InstallFormInput{
		Bundle:  testRebeccaBundle,
		Channel: "dev",
	})
	if errs.HasAny() {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(plan.Stages) != 1 {
		t.Fatalf("len(plan.Stages) = %d, want 1 (no configure step)", len(plan.Stages))
	}
	if plan.Stages[0].TolerateTimeout {
		t.Errorf("Rebecca install stage must not tolerate the remote timeout (it detaches)")
	}
	// Stdin provides 'y' to confirm the directory override, then hits EOF.
	if plan.Stages[0].AllocatePTY || plan.Stages[0].Input != "" {
		t.Errorf("Rebecca install stage must not manage stdin (got PTY=%v input=%q)",
			plan.Stages[0].AllocatePTY, plan.Stages[0].Input)
	}
	if !strings.Contains(plan.Stages[0].Command, `printf "y\n" | `) {
		t.Errorf("Rebecca install command must pipe confirmation so override prompt does not abort")
	}
	// Ports and PEM both travel base64-encoded inside the command, never via
	// stdin. Decode every embedded payload and assert the values are really there.
	decoded := decodeInstallPayloads(t, plan.Stages[0].Command)
	for _, want := range []string{`SERVICE_PORT = "62050"`, `XRAY_API_PORT = "62051"`, "BEGIN CERTIFICATE", "BEGIN RSA PRIVATE KEY"} {
		if !strings.Contains(decoded, want) {
			t.Errorf("decoded install payload missing %q\n--- decoded ---\n%s", want, decoded)
		}
	}
	if strings.Contains(plan.Stages[0].Command, "BEGIN CERTIFICATE") {
		t.Errorf("PEM must be embedded base64, not raw")
	}
	// No readback: the user supplied the bundle, nothing is read back.
	if plan.Readback.Command != "" {
		t.Errorf("Rebecca plan must have no readback command, got %q", plan.Readback.Command)
	}

	// Validation errors propagate as field-keyed i18n keys.
	_, errs = RebeccaProvider{}.BuildInstallPlan(InstallFormInput{Bundle: "bad"})
	if _, ok := errs["bundle"]; !ok {
		t.Errorf("expected bundle error from BuildInstallPlan, got %v", errs)
	}
}

// TestRebeccaInstallRunsWithoutBlockingStdin pins the non-interactive install
// contract against a faithful mini-fixture of the upstream code path:
//
//   - configure_binary_node_env skips read_node_certificate_bundle while the
//     cert files exist and are non-empty;
//   - prompt_node_port_setting defaults from the pre-written .env via
//     get_env_value, and only reads stdin when the value is missing.
//
// The fixture reads stdin with no PTY and no piped answers. If the pre-written
// .env were not honoured, the reads would hit EOF and the ports would be empty.
func TestRebeccaInstallRunsWithoutBlockingStdin(t *testing.T) {
	const fixture = `#!/usr/bin/env bash
set -e
APP_DIR="$1"; ENV_FILE="$1/.env"; CERT_FILE="$2"; CERT_KEY_FILE="$3"

is_rebecca_node_installed() {
    [ -d "$APP_DIR" ]
}

# The upstream script checks if APP_DIR exists; since Nodexia pre-creates it
# to write .env/certs, the override prompt is triggered and expects 'y'.
if is_rebecca_node_installed; then
    read -p "Do you want to override the previous installation? (y/n) " -r reply
    if [[ ! $reply =~ ^[Yy]$ ]]; then
        echo "Aborted installation" >&2
        exit 1
    fi
fi

get_env_value() {
    local key="$1"
    sed -n "s/^[[:space:]]*$key[[:space:]]*=[[:space:]]*\"\{0,1\}\([^\"]*\)\"\{0,1\}.*$/\1/p" "$ENV_FILE" | head -n 1
}
prompt_node_port_setting() {
    local key="$1" fallback="$2"
    local current_port value
    current_port=$(get_env_value "$key")
    fallback="${current_port:-$fallback}"
    IFS= read -r value || true
    echo "${value:-$fallback}"
}
read_node_certificate_bundle() { echo "CERT_PROMPT_RAN" >&2; }
configure_binary_node_env() {
    if [ ! -s "$CERT_FILE" ] || [ ! -s "$CERT_KEY_FILE" ]; then
        read_node_certificate_bundle
    fi
    SERVICE_PORT=$(prompt_node_port_setting SERVICE_PORT 62050)
    XRAY_API_PORT=$(prompt_node_port_setting XRAY_API_PORT 62051)
    echo "ports=$SERVICE_PORT/$XRAY_API_PORT"
}

SERVICE_PORT=$(prompt_node_port_setting SERVICE_PORT 62050)
XRAY_API_PORT=$(prompt_node_port_setting XRAY_API_PORT 62051)
echo "first-pass=$SERVICE_PORT/$XRAY_API_PORT"
configure_binary_node_env
`
	dir := t.TempDir()
	appDir := dir + "/node"
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir appDir: %v", err)
	}
	envPath := appDir + "/.env"
	certPath := dir + "/cert.pem"
	keyPath := dir + "/cert.key"
	script := "SERVICE_PORT = \"62990\"\nXRAY_API_PORT = \"62991\"\n"
	if err := os.WriteFile(envPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(certPath, []byte("cert"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	path := dir + "/fixture.sh"
	if err := os.WriteFile(path, []byte(fixture), 0o700); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// stdin is 'y\n': exactly what the generated install command pipes.
	cmd := exec.Command("bash", path, appDir, certPath, keyPath)
	cmd.Stdin = strings.NewReader("y\n")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture failed (install would block or error): %v\n%s", err, output)
	}
	text := string(output)
	if strings.Contains(text, "CERT_PROMPT_RAN") {
		t.Errorf("cert prompt ran even though the cert files were pre-written:\n%s", text)
	}
	for _, want := range []string{"first-pass=62990/62991", "ports=62990/62991"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q (ports must come from the pre-written .env):\n%s", want, text)
		}
	}
}

// decodeInstallPayloads base64-decodes every "echo <b64> | base64 -d" payload in
// a generated install command, so tests can assert on the real file contents
// without caring which payload is which.
func decodeInstallPayloads(t *testing.T, command string) string {
	t.Helper()
	pattern := regexp.MustCompile(`echo ([A-Za-z0-9+/=]+) \| base64 -d`)
	matches := pattern.FindAllStringSubmatch(command, -1)
	if len(matches) == 0 {
		t.Fatalf("install command embeds no base64 payloads:\n%s", command)
	}
	var out strings.Builder
	for _, match := range matches {
		raw, err := base64.StdEncoding.DecodeString(match[1])
		if err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		out.Write(raw)
		out.WriteString("\n")
	}
	return out.String()
}
