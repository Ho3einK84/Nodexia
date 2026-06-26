package analytics

import (
	"net/http"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// maxTagLength bounds a tag name on the limits admin form. It mirrors the
// server_tags storage, which holds short operator-chosen labels.
const maxTagLength = 64

// LimitsAdminHandler manages the fleet-level group/global monthly download caps
// at /analytics/limits. The global default and per-tag caps are the broader
// fallbacks resolved by ResolveEffectiveLimit (a per-server cap always wins).
type LimitsAdminHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewLimitsAdminHandler(deps module.Dependencies, repo Repository) LimitsAdminHandler {
	return LimitsAdminHandler{deps: deps, repo: repo}
}

// Page renders the limits admin page (GET /analytics/limits).
func (h LimitsAdminHandler) Page(w http.ResponseWriter, r *http.Request) {
	page := view.NewPageData(h.deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("analytics.limits.title")
	page.ActiveNav = "/analytics"
	page.ContentTemplate = "content-analytics-limits"
	page.PageTitle = page.T("analytics.limits.title")
	page.PageDescription = page.T("analytics.limits.description")
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.TrafficLimits = h.limitsView(r)
	if kind, msg := limitsFlash(r, page); kind != "" {
		page.FlashKind = kind
		page.FlashMessage = msg
	}
	page.PageStyles = []string{"/static/analytics.css"}
	page.PageScripts = []string{"/static/analytics.js"}

	if err := h.deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render limits page", http.StatusInternalServerError)
	}
}

// limitsView assembles the global default + per-tag rows for the admin page. On
// any read error it renders empty rather than failing the page.
func (h LimitsAdminHandler) limitsView(r *http.Request) view.TrafficLimitsView {
	v := view.TrafficLimitsView{
		GlobalAction:    "/analytics/limits",
		TagAction:       "/analytics/limits/tags",
		TagDeleteAction: "/analytics/limits/tags/delete",
		GlobalUnit:      defaultLimitUnit,
		UnitOptions:     limitUnitOptions,
	}
	if limit, ok, err := h.repo.GetScopedLimit(r.Context(), LimitScopeGlobal, ""); err == nil && ok {
		v.HasGlobal = true
		v.GlobalHuman = formatBytes(limit)
		v.GlobalValue, v.GlobalUnit = limitToValueUnit(limit)
	}
	if rules, err := h.repo.ListScopedLimits(r.Context()); err == nil {
		for _, rule := range rules {
			if rule.Scope != LimitScopeTag {
				continue
			}
			v.Tags = append(v.Tags, view.TrafficLimitTagView{
				Tag:        rule.Ref,
				LimitHuman: formatBytes(rule.LimitBytes),
			})
		}
	}
	return v
}

// SetGlobal handles POST /analytics/limits: an empty value clears the global
// default; a positive value sets it.
func (h LimitsAdminHandler) SetGlobal(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectLimits(w, r, "error=invalid")
		return
	}
	rawValue := strings.TrimSpace(r.FormValue("limit_value"))
	unit := strings.TrimSpace(r.FormValue("limit_unit"))
	if !validLimitUnit(unit) {
		unit = defaultLimitUnit
	}

	if rawValue == "" {
		if err := h.repo.DeleteScopedLimit(r.Context(), LimitScopeGlobal, ""); err != nil {
			redirectLimits(w, r, "error=save")
			return
		}
		redirectLimits(w, r, "flash=cleared")
		return
	}

	limitBytes, ok := parseLimitBytes(rawValue, unit)
	if !ok {
		redirectLimits(w, r, "error=positive")
		return
	}
	if err := h.repo.SetScopedLimit(r.Context(), LimitScopeGlobal, "", limitBytes); err != nil {
		redirectLimits(w, r, "error=save")
		return
	}
	redirectLimits(w, r, "flash=saved")
}

// SetTag handles POST /analytics/limits/tags: add or update a per-tag cap.
func (h LimitsAdminHandler) SetTag(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectLimits(w, r, "error=invalid")
		return
	}
	tag := strings.TrimSpace(r.FormValue("tag"))
	if tag == "" || len(tag) > maxTagLength {
		redirectLimits(w, r, "error=tag")
		return
	}
	rawValue := strings.TrimSpace(r.FormValue("limit_value"))
	unit := strings.TrimSpace(r.FormValue("limit_unit"))
	if !validLimitUnit(unit) {
		unit = defaultLimitUnit
	}
	limitBytes, ok := parseLimitBytes(rawValue, unit)
	if !ok {
		redirectLimits(w, r, "error=positive")
		return
	}
	if err := h.repo.SetScopedLimit(r.Context(), LimitScopeTag, tag, limitBytes); err != nil {
		redirectLimits(w, r, "error=save")
		return
	}
	redirectLimits(w, r, "flash=tag-saved")
}

// DeleteTag handles POST /analytics/limits/tags/delete: remove a per-tag cap.
func (h LimitsAdminHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectLimits(w, r, "error=invalid")
		return
	}
	tag := strings.TrimSpace(r.FormValue("tag"))
	if tag == "" {
		redirectLimits(w, r, "error=tag")
		return
	}
	if err := h.repo.DeleteScopedLimit(r.Context(), LimitScopeTag, tag); err != nil {
		redirectLimits(w, r, "error=save")
		return
	}
	redirectLimits(w, r, "flash=tag-removed")
}

func redirectLimits(w http.ResponseWriter, r *http.Request, query string) {
	http.Redirect(w, r, "/analytics/limits?"+query, http.StatusSeeOther)
}

// limitsFlash maps the ?flash=/?error= markers set after a POST onto a kind +
// localized message.
func limitsFlash(r *http.Request, page view.PageData) (kind, message string) {
	switch r.URL.Query().Get("flash") {
	case "saved":
		return "success", page.T("analytics.limits.flash_saved")
	case "cleared":
		return "success", page.T("analytics.limits.flash_cleared")
	case "tag-saved":
		return "success", page.T("analytics.limits.flash_tag_saved")
	case "tag-removed":
		return "success", page.T("analytics.limits.flash_tag_removed")
	}
	switch r.URL.Query().Get("error") {
	case "invalid":
		return "error", page.T("analytics.limit.error_invalid")
	case "positive":
		return "error", page.T("analytics.limit.error_positive")
	case "tag":
		return "error", page.T("analytics.limits.error_tag")
	case "save":
		return "error", page.T("analytics.limit.error_save")
	}
	return "", ""
}
