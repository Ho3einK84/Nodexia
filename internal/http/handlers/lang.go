package handlers

import (
	"net/http"
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
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//")
}

func equalHostNames(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	return a != "" && a == b
}
