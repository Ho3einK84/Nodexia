package i18n

import "context"

type contextKey struct{}

// NewContext returns a copy of ctx carrying loc. The locale middleware stores
// the request's resolved Localizer here; view.NewPageData reads it back.
func NewContext(ctx context.Context, loc *Localizer) context.Context {
	return context.WithValue(ctx, contextKey{}, loc)
}

// FromContext returns the Localizer stored in ctx, or nil when none is present
// (e.g. requests that bypass the locale middleware). Callers must tolerate nil
// and fall back to the default language.
func FromContext(ctx context.Context) *Localizer {
	loc, _ := ctx.Value(contextKey{}).(*Localizer)
	return loc
}
