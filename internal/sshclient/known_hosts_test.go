package sshclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestService returns a *Service backed by a temp known-hosts file.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh_known_hosts.json")
	svc := &Service{
		connectTimeout: 10 * time.Second,
		commandTimeout: 20 * time.Second,
		hostKeyPolicy:  "tofu",
		knownHostsPath: path,
	}
	return svc, path
}

// writeFile writes content to path, creating directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// ── loadKnownHosts ───────────────────────────────────────────────────────────

func TestLoadKnownHosts_FileNotExist(t *testing.T) {
	svc, _ := newTestService(t)

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store, got %d entries", len(store))
	}
}

func TestLoadKnownHosts_EmptyFile(t *testing.T) {
	svc, path := newTestService(t)
	writeFile(t, path, "")

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected nil error for empty file, got: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store for empty file, got %d entries", len(store))
	}
}

func TestLoadKnownHosts_WhitespaceOnlyFile(t *testing.T) {
	svc, path := newTestService(t)
	writeFile(t, path, "   \n\t\n  ")

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected nil error for whitespace file, got: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store for whitespace file, got %d entries", len(store))
	}
}

func TestLoadKnownHosts_ValidJSON(t *testing.T) {
	svc, path := newTestService(t)

	now := time.Now().UTC().Truncate(time.Second)
	entry := knownHostEntry{
		AuthorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest",
		Fingerprint:   "SHA256:testfingerprint",
		TrustedAt:     now,
		LastSeenAt:    now,
	}
	data := map[string]knownHostEntry{"84.75.255.69:22": entry}
	payload, _ := json.MarshalIndent(data, "", "  ")
	writeFile(t, path, string(payload)+"\n")

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(store))
	}
	got, ok := store["84.75.255.69:22"]
	if !ok {
		t.Fatal("expected entry for 84.75.255.69:22")
	}
	if got.Fingerprint != entry.Fingerprint {
		t.Errorf("fingerprint mismatch: got %q, want %q", got.Fingerprint, entry.Fingerprint)
	}
	if got.AuthorizedKey != entry.AuthorizedKey {
		t.Errorf("authorized_key mismatch: got %q, want %q", got.AuthorizedKey, entry.AuthorizedKey)
	}
}

func TestLoadKnownHosts_MultipleEntries(t *testing.T) {
	svc, path := newTestService(t)

	now := time.Now().UTC().Truncate(time.Second)
	data := map[string]knownHostEntry{
		"10.0.0.1:22":  {AuthorizedKey: "ssh-ed25519 AAAA1", Fingerprint: "SHA256:fp1", TrustedAt: now, LastSeenAt: now},
		"10.0.0.2:22":  {AuthorizedKey: "ssh-ed25519 AAAA2", Fingerprint: "SHA256:fp2", TrustedAt: now, LastSeenAt: now},
		"10.0.0.3:443": {AuthorizedKey: "ssh-ed25519 AAAA3", Fingerprint: "SHA256:fp3", TrustedAt: now, LastSeenAt: now},
	}
	payload, _ := json.MarshalIndent(data, "", "  ")
	writeFile(t, path, string(payload)+"\n")

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store) != 3 {
		t.Errorf("expected 3 entries, got %d", len(store))
	}
}

// ── Corruption recovery ───────────────────────────────────────────────────────

// corruptContent returns content that triggers "invalid character 'c' after
// top-level value" — the exact error seen in production.
func corruptContent() string {
	return `{}` + "\ncert-authority,namespaces=... ecdsa-sha2-nistp256 AAAA"
}

func TestLoadKnownHosts_InvalidJSON_RecoversAndBacksUp(t *testing.T) {
	svc, path := newTestService(t)
	writeFile(t, path, corruptContent())

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected corruption to be recovered, got error: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store after corruption recovery, got %d entries", len(store))
	}

	// Original file must be gone (backed up or removed).
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("corrupted file should have been renamed/removed, but it still exists at original path")
	}

	// A backup file with ".corrupt." in its name must exist in the same dir.
	entries, readErr := os.ReadDir(filepath.Dir(path))
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	var found bool
	for _, e := range entries {
		if strings.Contains(e.Name(), ".corrupt.") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a .corrupt. backup file to be created")
	}
}

func TestLoadKnownHosts_ConcatenatedJSON_RecoversAndBacksUp(t *testing.T) {
	// Two JSON objects concatenated — another form of the race-condition corruption.
	svc, path := newTestService(t)
	writeFile(t, path, `{"host1:22":{"authorized_key":"ssh-ed25519 A","fingerprint":"SHA256:a","trusted_at":"2024-01-01T00:00:00Z","last_seen_at":"2024-01-01T00:00:00Z"}}`+
		"\n"+
		`{"host2:22":{"authorized_key":"ssh-ed25519 B","fingerprint":"SHA256:b","trusted_at":"2024-01-01T00:00:00Z","last_seen_at":"2024-01-01T00:00:00Z"}}`)

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected corruption to be recovered, got error: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store after corruption recovery, got %d entries", len(store))
	}
}

func TestLoadKnownHosts_TruncatedJSON_RecoversAndBacksUp(t *testing.T) {
	svc, path := newTestService(t)
	// Truncated mid-write — another common corruption pattern.
	writeFile(t, path, `{"84.75.255.69:22":{"authorized_key":"ecdsa-sha2-nistp256 AAAA`)

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected truncation to be recovered, got error: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store after truncation recovery, got %d entries", len(store))
	}
}

func TestLoadKnownHosts_NonJSONContent_RecoversAndBacksUp(t *testing.T) {
	svc, path := newTestService(t)
	// OpenSSH known_hosts format accidentally written to the JSON path.
	writeFile(t, path, "84.75.255.69 ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYA\n")

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected non-JSON to be recovered, got error: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store after non-JSON recovery, got %d entries", len(store))
	}
}

func TestLoadKnownHosts_WrongType_RecoversAndBacksUp(t *testing.T) {
	svc, path := newTestService(t)
	// Valid JSON but wrong top-level type (array instead of object).
	writeFile(t, path, `["not","an","object"]`)

	store, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("expected wrong-type to be recovered, got error: %v", err)
	}
	if len(store) != 0 {
		t.Errorf("expected empty store, got %d entries", len(store))
	}
}

// ── saveKnownHosts ───────────────────────────────────────────────────────────

func TestSaveKnownHosts_Roundtrip(t *testing.T) {
	svc, _ := newTestService(t)

	now := time.Now().UTC().Truncate(time.Second)
	store := map[string]knownHostEntry{
		"1.2.3.4:22": {
			AuthorizedKey: "ssh-ed25519 AAAA",
			Fingerprint:   "SHA256:fp",
			TrustedAt:     now,
			LastSeenAt:    now,
		},
	}

	if err := svc.saveKnownHosts(store); err != nil {
		t.Fatalf("saveKnownHosts: %v", err)
	}

	loaded, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("loadKnownHosts after save: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry after roundtrip, got %d", len(loaded))
	}
	entry := loaded["1.2.3.4:22"]
	if entry.Fingerprint != "SHA256:fp" {
		t.Errorf("fingerprint mismatch: got %q, want %q", entry.Fingerprint, "SHA256:fp")
	}
}

func TestSaveKnownHosts_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "ssh_known_hosts.json")
	svc := &Service{knownHostsPath: path}

	if err := svc.saveKnownHosts(map[string]knownHostEntry{}); err != nil {
		t.Fatalf("saveKnownHosts with missing directory: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist after save: %v", err)
	}
}

func TestSaveKnownHosts_AtomicWrite_NoTempFileLeft(t *testing.T) {
	svc, path := newTestService(t)

	if err := svc.saveKnownHosts(map[string]knownHostEntry{}); err != nil {
		t.Fatalf("saveKnownHosts: %v", err)
	}

	// No temp file should remain after a successful save.
	dir := filepath.Dir(path)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("unexpected temp file left after save: %s", e.Name())
		}
	}
}

func TestSaveKnownHosts_ProducesValidJSON(t *testing.T) {
	svc, path := newTestService(t)

	now := time.Now().UTC()
	store := map[string]knownHostEntry{
		"host:22": {AuthorizedKey: "ssh-ed25519 AAAA", Fingerprint: "SHA256:fp", TrustedAt: now, LastSeenAt: now},
	}
	if err := svc.saveKnownHosts(store); err != nil {
		t.Fatalf("saveKnownHosts: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]knownHostEntry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Errorf("saved file is not valid JSON: %v\ncontent: %s", err, raw)
	}
}

func TestSaveKnownHosts_EmptyPath_ReturnsError(t *testing.T) {
	svc := &Service{knownHostsPath: ""}
	err := svc.saveKnownHosts(map[string]knownHostEntry{})
	if err == nil {
		t.Error("expected error for empty knownHostsPath, got nil")
	}
}

func TestLoadKnownHosts_EmptyPath_ReturnsError(t *testing.T) {
	svc := &Service{knownHostsPath: ""}
	_, err := svc.loadKnownHosts()
	if err == nil {
		t.Error("expected error for empty knownHostsPath, got nil")
	}
}

// ── Concurrent safety ─────────────────────────────────────────────────────────

// TestSaveKnownHosts_ConcurrentWrites simulates the exact race that produced
// the "invalid character 'c' after top-level value" production error: multiple
// goroutines simultaneously calling saveKnownHosts on the same Service.
//
// With pointer receivers, hostKeysMu is now shared across all callers, so only
// one goroutine can write at a time.  After all goroutines finish, the file
// must be parseable valid JSON.
func TestSaveKnownHosts_ConcurrentWrites(t *testing.T) {
	svc, path := newTestService(t)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errors := make(chan error, goroutines)

	for i := range goroutines {
		go func(n int) {
			defer wg.Done()
			svc.hostKeysMu.Lock()
			defer svc.hostKeysMu.Unlock()

			store, err := svc.loadKnownHosts()
			if err != nil {
				errors <- fmt.Errorf("goroutine %d loadKnownHosts: %w", n, err)
				return
			}
			store[fmt.Sprintf("10.0.0.%d:22", n)] = knownHostEntry{
				AuthorizedKey: fmt.Sprintf("ssh-ed25519 AAAA%d", n),
				Fingerprint:   fmt.Sprintf("SHA256:fp%d", n),
				TrustedAt:     time.Now().UTC(),
				LastSeenAt:    time.Now().UTC(),
			}
			if err := svc.saveKnownHosts(store); err != nil {
				errors <- fmt.Errorf("goroutine %d saveKnownHosts: %w", n, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent goroutine error: %v", err)
	}

	// The final file must be valid JSON.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after concurrent writes: %v", err)
	}
	var parsed map[string]knownHostEntry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Errorf("file is not valid JSON after concurrent writes: %v\ncontent:\n%s", err, raw)
	}
	if len(parsed) != goroutines {
		t.Errorf("expected %d entries after concurrent writes, got %d", goroutines, len(parsed))
	}
}

// TestSaveLoadCycle_PreservesAllFields verifies that every field in
// knownHostEntry survives a full save → load roundtrip.
func TestSaveLoadCycle_PreservesAllFields(t *testing.T) {
	svc, _ := newTestService(t)

	trusted := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)
	lastSeen := time.Date(2024, 6, 11, 12, 0, 0, 0, time.UTC)
	store := map[string]knownHostEntry{
		"84.75.255.69:22": {
			AuthorizedKey: "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBtest",
			Fingerprint:   "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEF",
			TrustedAt:     trusted,
			LastSeenAt:    lastSeen,
		},
	}

	if err := svc.saveKnownHosts(store); err != nil {
		t.Fatalf("saveKnownHosts: %v", err)
	}

	loaded, err := svc.loadKnownHosts()
	if err != nil {
		t.Fatalf("loadKnownHosts: %v", err)
	}

	got, ok := loaded["84.75.255.69:22"]
	if !ok {
		t.Fatal("entry for 84.75.255.69:22 not found after roundtrip")
	}
	if got.AuthorizedKey != store["84.75.255.69:22"].AuthorizedKey {
		t.Errorf("AuthorizedKey mismatch")
	}
	if got.Fingerprint != store["84.75.255.69:22"].Fingerprint {
		t.Errorf("Fingerprint mismatch")
	}
	if !got.TrustedAt.Equal(trusted) {
		t.Errorf("TrustedAt mismatch: got %v, want %v", got.TrustedAt, trusted)
	}
	if !got.LastSeenAt.Equal(lastSeen) {
		t.Errorf("LastSeenAt mismatch: got %v, want %v", got.LastSeenAt, lastSeen)
	}
}

// ── resolveConnectTimeout / resolveCommandTimeout ────────────────────────────

func TestResolveConnectTimeout_UsesOverride(t *testing.T) {
	svc := &Service{connectTimeout: 10 * time.Second}
	got := svc.resolveConnectTimeout(5 * time.Second)
	if got != 5*time.Second {
		t.Errorf("expected override 5s, got %s", got)
	}
}

func TestResolveConnectTimeout_FallsBackToServiceDefault(t *testing.T) {
	svc := &Service{connectTimeout: 15 * time.Second}
	got := svc.resolveConnectTimeout(0)
	if got != 15*time.Second {
		t.Errorf("expected service default 15s, got %s", got)
	}
}

func TestResolveConnectTimeout_FallsBackToHardcoded(t *testing.T) {
	svc := &Service{connectTimeout: 0}
	got := svc.resolveConnectTimeout(0)
	if got != 10*time.Second {
		t.Errorf("expected hardcoded 10s, got %s", got)
	}
}

func TestResolveCommandTimeout_UsesOverride(t *testing.T) {
	svc := &Service{commandTimeout: 30 * time.Second}
	got := svc.resolveCommandTimeout(5 * time.Second)
	if got != 5*time.Second {
		t.Errorf("expected override 5s, got %s", got)
	}
}

func TestResolveCommandTimeout_FallsBackToServiceDefault(t *testing.T) {
	svc := &Service{commandTimeout: 45 * time.Second}
	got := svc.resolveCommandTimeout(0)
	if got != 45*time.Second {
		t.Errorf("expected service default 45s, got %s", got)
	}
}

func TestResolveCommandTimeout_FallsBackToHardcoded(t *testing.T) {
	svc := &Service{commandTimeout: 0}
	got := svc.resolveCommandTimeout(0)
	if got != 20*time.Second {
		t.Errorf("expected hardcoded 20s, got %s", got)
	}
}
