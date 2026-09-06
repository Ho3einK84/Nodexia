package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

var (
	//go:embed web/templates/*.gohtml
	templateFiles embed.FS

	//go:embed web/static/*
	staticFiles embed.FS

	//go:embed schema.sql
	schemaSQL string
)

func Templates() fs.FS {
	return templateFiles
}

func Static() (fs.FS, error) {
	return fs.Sub(staticFiles, "web/static")
}

func Schema() string {
	return schemaSQL
}

var (
	staticVersionsOnce sync.Once
	staticVersions     map[string]string
)

// StaticAssetVersion returns a short, content-derived fingerprint for a file
// under web/static (named relative to the static root, e.g. "terminal.css"), or
// "" if the file is unknown. It is meant to be appended as a cache-busting query
// string (`/static/terminal.css?v=<fp>`): when a file's bytes change so does its
// URL, which forces browsers AND the stale-while-revalidate service worker to
// fetch the new copy instead of serving an old cached one — without any manual
// version bump. Hashes are computed once and cached for the life of the binary.
func StaticAssetVersion(name string) string {
	staticVersionsOnce.Do(func() {
		staticVersions = map[string]string{}
		sub, err := fs.Sub(staticFiles, "web/static")
		if err != nil {
			return
		}
		_ = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			f, openErr := sub.Open(path)
			if openErr != nil {
				return nil
			}
			defer func() {
				_ = f.Close()
			}()
			h := sha256.New()
			if _, copyErr := io.Copy(h, f); copyErr != nil {
				return nil
			}
			staticVersions[path] = hex.EncodeToString(h.Sum(nil)[:6])
			return nil
		})
	})
	return staticVersions[name]
}

// StaticAssetURL returns the root-relative URL for an embedded static asset,
// appending ?v=<fingerprint> if the asset exists. Accepts paths like "style.css",
// "/static/style.css", or "fonts/exo2-latin.woff2".
func StaticAssetURL(name string) string {
	clean := strings.TrimPrefix(name, "/static/")
	clean = strings.TrimPrefix(clean, "/")
	// Strip existing query string if any was provided
	if idx := strings.Index(clean, "?"); idx != -1 {
		clean = clean[:idx]
	}
	v := StaticAssetVersion(clean)
	if v != "" {
		return "/static/" + clean + "?v=" + v
	}
	if strings.HasPrefix(name, "/") {
		return name
	}
	return "/static/" + name
}

// StaticAssetsDigest returns an aggregate fingerprint (12 hex characters)
// representing the content of all embedded static assets. It changes whenever
// any static file is modified, and can be used to version service worker caches.
func StaticAssetsDigest() string {
	// ensure staticVersions is computed
	_ = StaticAssetVersion("style.css")
	h := sha256.New()
	keys := make([]string, 0, len(staticVersions))
	for k := range staticVersions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(staticVersions[k]))
	}
	return hex.EncodeToString(h.Sum(nil)[:6])
}
