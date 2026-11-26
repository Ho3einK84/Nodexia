package analytics

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/humanize"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// maxTagLength bounds a tag name on the limits admin form. It mirrors the
// server_tags storage, which holds short operator-chosen labels.
const maxTagLength = 64

type fleetTagRepo interface {
	ListFleetTagCounts(ctx context.Context) (map[string]int, error)
}

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

// limitsView assembles the global default + per-tag rows + fleet tag suggestions
// for the admin page. On any read error it renders empty rather than failing.
func (h LimitsAdminHandler) limitsView(r *http.Request) view.TrafficLimitsView {
	ctx := r.Context()
	v := view.TrafficLimitsView{
		GlobalAction:    "/analytics/limits",
		TagAction:       "/analytics/limits/tags",
		TagDeleteAction: "/analytics/limits/tags/delete",
		GlobalUnit:      defaultLimitUnit,
		UnitOptions:     limitUnitOptions,
	}
	tagCounts := map[string]int{}
	if tr, ok := h.repo.(fleetTagRepo); ok {
		if m, err := tr.ListFleetTagCounts(ctx); err == nil {
			tagCounts = m
		}
	}
	cappedTags := make(map[string]bool)
	if limit, ok, err := h.repo.GetScopedLimit(ctx, LimitScopeGlobal, ""); err == nil && ok {
		v.HasGlobal = true
		v.GlobalHuman = humanize.Bytes(limit)
		v.GlobalValue, v.GlobalUnit = limitToValueUnit(limit)
	}
	if rules, err := h.repo.ListScopedLimits(ctx); err == nil {
		for _, rule := range rules {
			if rule.Scope != LimitScopeTag {
				continue
			}
			valStr, unitStr := limitToValueUnit(rule.LimitBytes)
			cappedTags[rule.Ref] = true
			v.Tags = append(v.Tags, view.TrafficLimitTagView{
				Tag:         rule.Ref,
				LimitHuman:  humanize.Bytes(rule.LimitBytes),
				ValueInput:  valStr,
				UnitInput:   unitStr,
				ServerCount: tagCounts[rule.Ref],
			})
		}
	}
	if len(tagCounts) > 0 {
		names := make([]string, 0, len(tagCounts))
		for name := range tagCounts {
			names = append(names, name)
		}
		sort.Strings(names)
		v.KnownTags = make([]view.FleetTagSuggestionView, 0, len(names))
		for _, name := range names {
			v.KnownTags = append(v.KnownTags, view.FleetTagSuggestionView{
				Tag:         name,
				ServerCount: tagCounts[name],
				HasCap:      cappedTags[name],
			})
		}
	}
	return v
}

// SetGlobal handles POST /analytics/limits: an empty value clears the global
// default; a positive value sets it.
func (h LimitsAdminHandler) SetGlobal(w http.ResponseWriter, r *http.Request) {
	asJSON := wantsJSON(r)
	if err := r.ParseForm(); err != nil {
		h.respondLimitsError(w, r, asJSON, "invalid", "analytics.limit.error_invalid")
		return
	}
	rawValue := strings.TrimSpace(r.FormValue("limit_value"))
	unit := strings.TrimSpace(r.FormValue("limit_unit"))
	if !validLimitUnit(unit) {
		unit = defaultLimitUnit
	}

	if rawValue == "" {
		if err := h.repo.DeleteScopedLimit(r.Context(), LimitScopeGlobal, ""); err != nil {
			h.respondLimitsError(w, r, asJSON, "save", "analytics.limit.error_save")
			return
		}
		h.respondLimitsSuccess(w, r, asJSON, "cleared", "analytics.limits.flash_cleared")
		return
	}

	limitBytes, ok := parseLimitBytes(rawValue, unit)
	if !ok {
		h.respondLimitsError(w, r, asJSON, "positive", "analytics.limit.error_positive")
		return
	}
	if err := h.repo.SetScopedLimit(r.Context(), LimitScopeGlobal, "", limitBytes); err != nil {
		h.respondLimitsError(w, r, asJSON, "save", "analytics.limit.error_save")
		return
	}
	h.respondLimitsSuccess(w, r, asJSON, "saved", "analytics.limits.flash_saved")
}

// SetTag handles POST /analytics/limits/tags: add or update a per-tag cap.
func (h LimitsAdminHandler) SetTag(w http.ResponseWriter, r *http.Request) {
	asJSON := wantsJSON(r)
	if err := r.ParseForm(); err != nil {
		h.respondLimitsError(w, r, asJSON, "invalid", "analytics.limit.error_invalid")
		return
	}
	tag := strings.TrimSpace(r.FormValue("tag"))
	if tag == "" || len(tag) > maxTagLength {
		h.respondLimitsError(w, r, asJSON, "tag", "analytics.limits.error_tag")
		return
	}
	rawValue := strings.TrimSpace(r.FormValue("limit_value"))
	unit := strings.TrimSpace(r.FormValue("limit_unit"))
	if !validLimitUnit(unit) {
		unit = defaultLimitUnit
	}
	limitBytes, ok := parseLimitBytes(rawValue, unit)
	if !ok {
		h.respondLimitsError(w, r, asJSON, "positive", "analytics.limit.error_positive")
		return
	}
	if err := h.repo.SetScopedLimit(r.Context(), LimitScopeTag, tag, limitBytes); err != nil {
		h.respondLimitsError(w, r, asJSON, "save", "analytics.limit.error_save")
		return
	}
	h.respondLimitsSuccess(w, r, asJSON, "tag-saved", "analytics.limits.flash_tag_saved")
}

// DeleteTag handles POST /analytics/limits/tags/delete: remove a per-tag cap.
func (h LimitsAdminHandler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	asJSON := wantsJSON(r)
	if err := r.ParseForm(); err != nil {
		h.respondLimitsError(w, r, asJSON, "invalid", "analytics.limit.error_invalid")
		return
	}
	tag := strings.TrimSpace(r.FormValue("tag"))
	if tag == "" {
		h.respondLimitsError(w, r, asJSON, "tag", "analytics.limits.error_tag")
		return
	}
	if err := h.repo.DeleteScopedLimit(r.Context(), LimitScopeTag, tag); err != nil {
		h.respondLimitsError(w, r, asJSON, "save", "analytics.limit.error_save")
		return
	}
	h.respondLimitsSuccess(w, r, asJSON, "tag-removed", "analytics.limits.flash_tag_removed")
}

func (h LimitsAdminHandler) respondLimitsError(w http.ResponseWriter, r *http.Request, asJSON bool, errCode, errKey string) {
	if asJSON {
		page := view.NewPageData(h.deps.Config, r)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"ok":      false,
			"message": page.T(errKey),
		})
		return
	}
	redirectLimits(w, r, "error="+errCode)
}

func (h LimitsAdminHandler) respondLimitsSuccess(w http.ResponseWriter, r *http.Request, asJSON bool, flashCode, msgKey string) {
	if asJSON {
		page := view.NewPageData(h.deps.Config, r)
		lv := h.limitsView(r)
		badgeText := page.T("analytics.limit.none")
		badgeVariant := "none"
		if lv.HasGlobal {
			badgeText = page.T("analytics.limit.current", "value", lv.GlobalHuman)
			badgeVariant = "active"
		}
		tags := lv.Tags
		if tags == nil {
			tags = []view.TrafficLimitTagView{}
		}
		known := lv.KnownTags
		if known == nil {
			known = []view.FleetTagSuggestionView{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"message":       page.T(msgKey),
			"has_global":    lv.HasGlobal,
			"global_human":  lv.GlobalHuman,
			"global_value":  lv.GlobalValue,
			"global_unit":   lv.GlobalUnit,
			"badge_text":    badgeText,
			"badge_variant": badgeVariant,
			"tags":          tags,
			"known_tags":    known,
		})
		return
	}
	redirectLimits(w, r, "flash="+flashCode)
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
