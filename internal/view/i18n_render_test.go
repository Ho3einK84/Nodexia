package view

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

// requestWithLang builds a GET request whose context carries a localizer for
// the given language code, mimicking what the locale middleware does.
func requestWithLang(t *testing.T, code string) *http.Request {
	t.Helper()
	loc := i18n.MustDefault().Localizer(code)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	return r.WithContext(i18n.NewContext(r.Context(), loc))
}

func renderHome(t *testing.T, code string) string {
	t.Helper()
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	page := NewPageData(config.Config{App: config.AppConfig{Name: "Nodexia"}}, requestWithLang(t, code))
	page.ContentTemplate = "content-home"
	page.PageTitle = page.T("home.page_title")

	rec := httptest.NewRecorder()
	if err := renderer.Render(rec, http.StatusOK, page); err != nil {
		t.Fatalf("Render(%s): %v", code, err)
	}
	return rec.Body.String()
}

func TestHomeRendersEnglishLTR(t *testing.T) {
	body := renderHome(t, "en")
	wants := []string{
		`<html lang="en" dir="ltr">`,
		"Operations overview", // localized page title
		"Servers",             // nav, translated key resolves to English
		"Monitored servers",   // home KPI
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("english home missing %q", want)
		}
	}
	if strings.Contains(body, "home.kpi.monitored_servers") {
		t.Error("english home leaked a raw translation key")
	}
}

func TestHomeRendersPersianRTL(t *testing.T) {
	body := renderHome(t, "fa")
	wants := []string{
		`<html lang="fa" dir="rtl">`, // direction flips for Persian
		"نمای کلی عملیات",            // localized page title
		"سرورها",                     // nav servers
		"سرورهای تحت پایش",           // home KPI
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("persian home missing %q", want)
		}
	}
}

func TestLanguageSwitcherRendersAllLocales(t *testing.T) {
	body := renderHome(t, "en")
	for _, want := range []string{`href="/lang/en"`, `href="/lang/fa"`, "فارسی"} {
		if !strings.Contains(body, want) {
			t.Errorf("language switcher missing %q", want)
		}
	}
}
