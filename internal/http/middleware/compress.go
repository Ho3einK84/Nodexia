package middleware

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

var gzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// Compress applies gzip response compression to text, HTML, JSON, JS, CSS, and
// SVG responses when the client advertises Accept-Encoding: gzip. WebSockets,
// SSE streams, Range requests, attachments, and already-compressed binary
// formats bypass compression automatically.
func Compress() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipCompression(r) {
				next.ServeHTTP(w, r)
				return
			}

			grw := &gzipResponseWriter{ResponseWriter: w}
			defer grw.Close()
			next.ServeHTTP(grw, r)
		})
	}
}

func shouldSkipCompression(r *http.Request) bool {
	if r.Method == http.MethodHead {
		return true
	}
	if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		return true
	}
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	if r.Header.Get("Range") != "" {
		return true
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		return true
	}
	switch strings.ToLower(path.Ext(r.URL.Path)) {
	case ".woff2", ".woff", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".zip", ".gz", ".tgz", ".br", ".pdf", ".mp4", ".webm":
		return true
	}
	return false
}

func acceptsGzip(header string) bool {
	if header == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		encoding, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(encoding), "gzip") {
			continue
		}
		if params == "" {
			return true
		}
		for _, param := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(param), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q <= 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

func isCompressibleContentType(ct string) bool {
	if ct == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(ct, ";")[0])
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "text/event-stream" {
		return false
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/javascript",
		"application/x-javascript",
		"application/json",
		"application/manifest+json",
		"application/xml",
		"application/xhtml+xml",
		"image/svg+xml":
		return true
	default:
		return false
	}
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	decided     bool
	compress    bool
	wroteHeader bool
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *gzipResponseWriter) decide(statusCode int, sniff []byte) {
	if w.decided {
		return
	}
	w.decided = true

	// 1xx, 204, 304 must never carry a body or Content-Encoding.
	if (statusCode >= 100 && statusCode < 200) || statusCode == http.StatusNoContent || statusCode == http.StatusNotModified {
		return
	}

	hdr := w.Header()
	if hdr.Get("Content-Encoding") != "" {
		return
	}
	if disp := strings.ToLower(hdr.Get("Content-Disposition")); strings.Contains(disp, "attachment") {
		return
	}

	ct := hdr.Get("Content-Type")
	if ct == "" && len(sniff) > 0 {
		ct = http.DetectContentType(sniff)
	}
	if !isCompressibleContentType(ct) {
		return
	}

	w.compress = true
	hdr.Del("Content-Length")
	hdr.Set("Content-Encoding", "gzip")
	appendVary(hdr, "Accept-Encoding")

	gz := gzipWriterPool.Get().(*gzip.Writer)
	gz.Reset(w.ResponseWriter)
	w.gz = gz
}

func appendVary(hdr http.Header, value string) {
	for _, existing := range hdr.Values("Vary") {
		for _, tok := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), value) {
				return
			}
		}
	}
	hdr.Add("Vary", value)
}

func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.decide(statusCode, nil)
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.decide(http.StatusOK, p)
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
	if w.compress && w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *gzipResponseWriter) Flush() {
	if w.compress && w.gz != nil {
		_ = w.gz.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gzipResponseWriter) Close() {
	if w.gz != nil {
		_ = w.gz.Close()
		w.gz.Reset(io.Discard)
		gzipWriterPool.Put(w.gz)
		w.gz = nil
	}
}
