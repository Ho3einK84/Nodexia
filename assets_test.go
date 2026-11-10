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
	for _, assetName := range []string{"tokens.css", "shell.css", "style.css"} {
		u := StaticAssetURL(assetName)
		prefix := "/static/" + assetName + "?v="
		if !strings.HasPrefix(u, prefix) {
			t.Fatalf("unexpected StaticAssetURL for %s: %s (expected prefix %s)", assetName, u, prefix)
		}
	}
}

func TestCSSBraceBalance(t *testing.T) {
	sub, err := fs.Sub(staticFiles, "web/static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}

	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".css") {
			return nil
		}
		data, readErr := fs.ReadFile(sub, path)
		if readErr != nil {
			t.Fatalf("ReadFile %s: %v", path, readErr)
		}
		content := string(data)
		// Strip comments
		var cleaned strings.Builder
		inComment := false
		for i := 0; i < len(content); i++ {
			if !inComment && i+1 < len(content) && content[i] == '/' && content[i+1] == '*' {
				inComment = true
				i++
				continue
			}
			if inComment && i+1 < len(content) && content[i] == '*' && content[i+1] == '/' {
				inComment = false
				i++
				continue
			}
			if !inComment {
				cleaned.WriteByte(content[i])
			}
		}
		s := cleaned.String()
		opens := strings.Count(s, "{")
		closes := strings.Count(s, "}")
		if opens != closes {
			t.Errorf("CSS brace mismatch in %s: %d opens vs %d closes", path, opens, closes)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

func TestJSSyntaxWithNode(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if _, errHermes := exec.LookPath("/root/.hermes/tools/node-26.12.1-linux-x64/bin/node"); errHermes == nil {
			nodePath = "/root/.hermes/tools/node-26.12.1-linux-x64/bin/node"
		} else {
			t.Skip("node not found in PATH, skipping JS syntax check")
		}
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
