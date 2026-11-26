package middleware_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
)

func TestCompressGzipHTML(t *testing.T) {
	payload := strings.Repeat("<p>Nodexia dashboard content</p>\n", 50)
	handler := middleware.Compress()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, payload)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", vary)
	}
	if rec.Body.Len() >= len(payload) {
		t.Fatalf("compressed len %d >= raw len %d", rec.Body.Len(), len(payload))
	}

	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() { _ = zr.Close() }()
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(decoded) != payload {
		t.Fatal("decompressed body mismatch")
	}
}

func TestCompressSkipsSSEAndBinaryAndAttachments(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		accept string
		ct     string
		disp   string
	}{
		{name: "sse-accept", path: "/events", accept: "text/event-stream", ct: "text/event-stream"},
		{name: "sse-ct", path: "/events", accept: "*/*", ct: "text/event-stream"},
		{name: "woff2-ext", path: "/static/fonts/inter.woff2", ct: "font/woff2"},
		{name: "png-ct", path: "/img", ct: "image/png"},
		{name: "attachment", path: "/export", ct: "application/json", disp: `attachment; filename="backup.json"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := middleware.Compress()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.ct != "" {
					w.Header().Set("Content-Type", tc.ct)
				}
				if tc.disp != "" {
					w.Header().Set("Content-Disposition", tc.disp)
				}
				_, _ = io.WriteString(w, "raw-body-payload")
			}))

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("expected no Content-Encoding, got %q", got)
			}
			if rec.Body.String() != "raw-body-payload" {
				t.Fatalf("body = %q, want raw-body-payload", rec.Body.String())
			}
		})
	}
}

func TestCompressHonorsQZero(t *testing.T) {
	handler := middleware.Compress()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0, identity")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("expected no gzip when q=0, got %q", got)
	}
}
