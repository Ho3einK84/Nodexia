package assets

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticAssetsDigest(t *testing.T) {
	d := StaticAssetsDigest()
	if len(d) != 12 {
		t.Fatalf("expected 12-character digest, got %q", d)
	}
}

func TestStaticAssetURL(t *testing.T) {
	u := StaticAssetURL("style.css")
	if !strings.HasPrefix(u, "/static/style.css?v=") {
		t.Fatalf("unexpected StaticAssetURL: %s", u)
	}
}

func TestJSSyntaxWithNode(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found in PATH, skipping JS syntax check")
	}

	sub, err := fs.Sub(staticFiles, "web/static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}

	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		fullPath := filepath.Join("web", "static", path)
		cmd := exec.Command(nodePath, "--check", fullPath)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Errorf("JS syntax error in %s: %v\nOutput: %s", fullPath, runErr, string(out))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}
