package i18n

import (
	"sort"
	"strconv"
	"strings"
)

// CookieName is the cookie that persists the user's explicit language choice.
// It is not HttpOnly because it carries only a public UI preference and no
// security value, but it is SameSite=Lax like the rest of the app's cookies.
const CookieName = "nodexia_lang"

// Resolve picks the active language code for a request, in priority order:
//  1. an explicit, still-supported choice persisted in the language cookie;
//  2. the best match from the Accept-Language header on first visit;
//  3. the default language (English).
//
// It always returns a code the bundle has a catalog for.
func (b *Bundle) Resolve(cookieValue, acceptLanguage string) string {
	if code := strings.TrimSpace(cookieValue); code != "" && b.HasLanguage(code) {
		return code
	}
	if code := b.matchAcceptLanguage(acceptLanguage); code != "" {
		return code
	}
	return DefaultLanguage
}

// matchAcceptLanguage returns the highest-quality Accept-Language entry the
// bundle supports, comparing on the primary subtag (so "fa-IR" matches "fa").
// It returns "" when nothing matches.
func (b *Bundle) matchAcceptLanguage(header string) string {
	type ranged struct {
		code    string
		quality float64
		order   int
	}

	var candidates []ranged
	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag := part
		quality := 1.0
		if semi := strings.Index(part, ";"); semi >= 0 {
			tag = strings.TrimSpace(part[:semi])
			if q := parseQuality(part[semi+1:]); q >= 0 {
				quality = q
			}
		}
		if tag == "*" || tag == "" {
			continue
		}
		primary := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
		candidates = append(candidates, ranged{code: primary, quality: quality, order: i})
	}

	// Stable sort by quality desc, then original order, so equal-quality tags
	// keep the header's ordering.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].quality != candidates[j].quality {
			return candidates[i].quality > candidates[j].quality
		}
		return candidates[i].order < candidates[j].order
	})

	for _, c := range candidates {
		if b.HasLanguage(c.code) {
			return c.code
		}
	}
	return ""
}

// parseQuality extracts the q-value from an Accept-Language parameter segment
// such as "q=0.8". It returns -1 when no valid q-value is present.
func parseQuality(segment string) float64 {
	for _, param := range strings.Split(segment, ";") {
		param = strings.TrimSpace(param)
		if !strings.HasPrefix(param, "q=") {
			continue
		}
		if q, err := strconv.ParseFloat(strings.TrimPrefix(param, "q="), 64); err == nil {
			return q
		}
	}
	return -1
}
