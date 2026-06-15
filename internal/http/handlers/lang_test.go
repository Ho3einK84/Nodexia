package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

func newLangHandler(t *testing.T) LangHandler {
	t.Helper()
	bundle, err := i18n.Default()
	if err != nil {
		t.Fatalf("i18n.Default: %v", err)
	}
	return NewLangHandler(bundle, false)
}

// serve drives the handler with a /lang/{code} request, wiring the path value
// the way the ServeMux pattern would.
func serve(t *testing.T, h LangHandler, code, referer, returnParam string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/lang/" + code
	if returnParam != "" {
		target += "?return=" + returnParam
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.SetPathValue("code", code)
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestLangHandlerSetsCookieAndRedirects(t *testing.T) {
	h := newLangHandler(t)
	rec := serve(t, h, "fa", "http://example.com/servers", "")

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/servers" {
		t.Errorf("redirect = %q, want /servers", got)
	}

	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == i18n.CookieName {
			found = true
			if c.Value != "fa" {
				t.Errorf("cookie value = %q, want fa", c.Value)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
			}
		}
	}
	if !found {
		t.Errorf("language cookie %q was not set", i18n.CookieName)
	}
}

func TestLangHandlerRejectsUnknownLanguage(t *testing.T) {
	h := newLangHandler(t)
	rec := serve(t, h, "zz", "", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown language status = %d, want 404", rec.Code)
	}
}

func TestLangHandlerRejectsOpenRedirect(t *testing.T) {
	h := newLangHandler(t)
	// Off-site referer and a scheme-relative ?return= must both be ignored.
	rec := serve(t, h, "en", "http://evil.test/phish", "//evil.test/phish")
	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("redirect = %q, want / (open redirect blocked)", got)
	}
}

func TestLangHandlerReturnParamWins(t *testing.T) {
	h := newLangHandler(t)
	rec := serve(t, h, "en", "http://example.com/servers", "/alerts")
	if got := rec.Header().Get("Location"); got != "/alerts" {
		t.Errorf("redirect = %q, want /alerts", got)
	}
}
