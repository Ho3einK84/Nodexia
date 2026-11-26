package assets

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
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
	modURL := StaticModuleURL("/static/app.js")
	wantMod := "/static/v/" + StaticAssetsDigest() + "/app.js"
	if modURL != wantMod {
		t.Fatalf("StaticModuleURL(/static/app.js) = %q, want %q", modURL, wantMod)
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

func TestServiceWorkerPrecacheFilesExist(t *testing.T) {
	sub, err := fs.Sub(staticFiles, "web/static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	raw, err := fs.ReadFile(sub, "sw.js")
	if err != nil {
		t.Fatalf("ReadFile sw.js: %v", err)
	}
	script := string(raw)
	start := strings.Index(script, "PRECACHE_URLS = [")
	if start < 0 {
		t.Fatal("sw.js missing PRECACHE_URLS")
	}
	end := strings.Index(script[start:], "];")
	if end < 0 {
		t.Fatal("sw.js PRECACHE_URLS unterminated")
	}
	block := script[start : start+end]
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, "'/static/")
		if idx < 0 {
			continue
		}
		rest := line[idx+len("'/static/"):]
		closeIdx := strings.Index(rest, "'")
		if closeIdx < 0 {
			continue
		}
		rel := rest[:closeIdx]
		if _, err := fs.Stat(sub, rel); err != nil {
			t.Errorf("sw.js PRECACHE_URLS references missing asset %q: %v", rel, err)
		}
	}
}

var unscopedLucideRe = regexp.MustCompile(`createIcons\s*\(\s*\)`)

func TestNoUnscopedLucideCreateIcons(t *testing.T) {
	sub, err := fs.Sub(staticFiles, "web/static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	allowed := map[string]bool{
		"modules/core.js": true,
		"offline.js":      true,
	}
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		if strings.HasSuffix(path, ".min.js") || allowed[path] {
			return nil
		}
		data, readErr := fs.ReadFile(sub, path)
		if readErr != nil {
			t.Fatalf("ReadFile %s: %v", path, readErr)
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
				continue
			}
			if unscopedLucideRe.MatchString(line) {
				t.Errorf("%s:%d uses unscoped createIcons(); pass { root: scope } or use window.nxRenderIcons(scope)", path, lineNo+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

var templateLucideIconRe = regexp.MustCompile(`data-lucide="([a-z0-9-]+)"`)

func kebabToPascal(s string) string {
	var b strings.Builder
	for _, part := range strings.Split(s, "-") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

func TestLucideSubsetCoversAllTemplates(t *testing.T) {
	lucideData, err := fs.ReadFile(staticFiles, "web/static/lucide.min.js")
	if err != nil {
		t.Fatalf("ReadFile lucide.min.js: %v", err)
	}
	if len(lucideData) > 100*1024 {
		t.Fatalf("lucide.min.js is %d bytes (>100 KiB); keep it trimmed to the used icon subset", len(lucideData))
	}
	lucideSrc := string(lucideData)

	tplSub, err := fs.Sub(templateFiles, "web/templates")
	if err != nil {
		t.Fatalf("fs.Sub templates: %v", err)
	}
	checked := 0
	err = fs.WalkDir(tplSub, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".gohtml") {
			return nil
		}
		data, readErr := fs.ReadFile(tplSub, path)
		if readErr != nil {
			t.Fatalf("ReadFile %s: %v", path, readErr)
		}
		for _, m := range templateLucideIconRe.FindAllStringSubmatch(string(data), -1) {
			iconName := m[1]
			pascal := kebabToPascal(iconName)
			checked++
			if !strings.Contains(lucideSrc, `"`+pascal+`":[`) {
				t.Errorf("template %s references Lucide icon %q (PascalCase %q) missing from web/static/lucide.min.js", path, iconName, pascal)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir templates: %v", err)
	}
	if checked == 0 {
		t.Fatal("expected to verify at least one data-lucide icon in templates")
	}
}
