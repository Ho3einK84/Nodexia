package handlers

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

// LangHandler persists an explicit language choice in a cookie and redirects
// the user back to the page they came from. It is reached via GET /lang/{code}
// from the header switcher; switching is a harmless UI preference (no security
// value), so it is exempt from CSRF like other safe-method routes.
type LangHandler struct {
	bundle       *i18n.Bundle
	cookieSecure bool
}

func NewLangHandler(bundle *i18n.Bundle, cookieSecure bool) LangHandler {
	return LangHandler{bundle: bundle, cookieSecure: cookieSecure}
}

func (h LangHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.PathValue("code"))
	if !h.bundle.HasLanguage(code) {
		http.NotFound(w, r)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     i18n.CookieName,
		Value:    code,
		Path:     "/",
		MaxAge:   int((365 * 24 * 60 * 60)),
		HttpOnly: false, // a public UI preference; readable by client code if needed
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, safeReturnTarget(r), http.StatusSeeOther)
}

// safeReturnTarget returns a same-origin path to redirect back to after a
// language switch. It prefers an explicit ?return= path, then the Referer, and
// falls back to "/". Only same-origin, root-relative paths are honoured so the
// switch link cannot be abused as an open redirect.
func safeReturnTarget(r *http.Request) string {
	if candidate := r.URL.Query().Get("return"); isSafeLocalPath(candidate) {
		return candidate
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if parsed, err := url.Parse(ref); err == nil {
			if equalHostNames(parsed.Host, r.Host) {
				target := parsed.EscapedPath()
				if parsed.RawQuery != "" {
					target += "?" + parsed.RawQuery
				}
				if isSafeLocalPath(target) {
					return target
				}
			}
		}
	}
	return "/"
}

// isSafeLocalPath reports whether p is a root-relative path ("/foo"), excluding
// scheme-relative ("//host") and absolute URLs that could redirect off-site.
func isSafeLocalPath(p string) bool {
	if p == "" || strings.TrimSpace(p) != p {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	lower := strings.ToLower(p)
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") ||
		strings.HasPrefix(lower, "/%2f") || strings.HasPrefix(lower, "/%5c") {
		return false
	}
	parsed, err := url.Parse(p)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return false
	}
	return parsed.Path != "" && strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(parsed.Path, "//")
}

func equalHostNames(a, b string) bool {
	na, okA := normalizeRedirectHost(a)
	nb, okB := normalizeRedirectHost(b)
	return okA && okB && na == nb
}

func normalizeRedirectHost(raw string) (string, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || strings.ContainsAny(raw, "@/?\\ \t\r\n") {
		return "", false
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
			inner := strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
			ip, parseErr := netip.ParseAddr(inner)
			if parseErr != nil {
				return "", false
			}
			return ip.String(), true
		}
		if strings.Contains(raw, ":") {
			ip, parseErr := netip.ParseAddr(raw)
			if parseErr != nil {
				return "", false
			}
			return ip.String(), true
		}
		return raw, true
	}
	if host == "" {
		return "", false
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		host = ip.String()
	}
	if port == "" {
		return host, true
	}
	return net.JoinHostPort(host, port), true
}
