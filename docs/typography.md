# Typography

Nodexia's type system uses **Exo 2** as the primary family. This document
records the analysis and the decisions behind the migration so the system stays
consistent as new modules are added.

## Why Exo 2

The product is a self-hosted control panel for monitoring and managing network
nodes — a professional infrastructure platform. The design direction is
**modern, slightly futuristic, clean and premium, distinctive without
sacrificing readability**.

Exo 2 fits that brief: it is a geometric, technical sans with squared-off
terminals and open counters that reads as engineered rather than generic, while
remaining highly legible at the small sizes the dashboards, tables and logs rely
on. It replaces Inter, which is excellent but ubiquitous and gives the product
no distinctive identity.

## Self-hosting

Fonts are **self-hosted**, matching the project's deployment model: a single Go
binary that embeds `web/static` and serves everything from its own origin with
no third-party requests. This keeps the app fully functional on air-gapped /
intranet deployments, avoids a render-blocking round-trip to a CDN, and keeps
the Content-Security-Policy tight (`font-src 'self'`).

Exo 2's Latin set is delivered by Google Fonts as a **single variable-font
WOFF2** whose weight axis spans 100–900, so the entire 400–800 range we use is
covered by **one ~40 KB file** (`exo2-latin.woff2`). The `latin-ext` subset
(~30 KB) is a second `@font-face` gated by its own `unicode-range`, so it is
only fetched if a glyph outside Basic Latin actually appears. No per-weight
files, no unused styles.

Loading is optimized as follows:

- **Variable font** → one network request covers every weight.
- `font-display: swap` → text paints immediately in the fallback and swaps in
  Exo 2 when ready; no invisible-text flash.
- `<link rel="preload" as="font">` for the Latin WOFF2 in `<head>` so the
  download starts in parallel with the stylesheet instead of after it.
- The service worker precaches `exo2-latin.woff2` (cache version bumped) so
  repeat visits and offline use are instant.
- The font files are served with `Cache-Control: public, max-age=1y, immutable`
  (they are content-stable under a versioned name), unlike the revalidated
  CSS/JS.

The fallback stack (`'Exo 2', ui-sans-serif, system-ui, -apple-system, …`) keeps
metrics close while the font loads. Monospace stacks (terminal, code, logs,
file listings) are **unchanged** — code and command output must stay monospaced.

## Weight hierarchy

Exo 2 is wired in as a variable font and surfaced through design tokens in
`:root`. The required structural hierarchy uses **SemiBold (600) / Bold (700) /
ExtraBold (800)**; two lighter weights carry body and controls so the heavier
weights actually stand out (an all-bold UI flattens hierarchy and hurts
scanning).

| Token              | Weight | Used for                                                                 |
| ------------------ | ------ | ------------------------------------------------------------------------ |
| `--fw-regular`     | 400    | Body copy, descriptions, table cell data, log/help text, paragraphs      |
| `--fw-medium`      | 500    | Interactive controls (buttons, inputs, selects), secondary labels, nav   |
| `--fw-semibold`    | 600    | Eyebrows, table headers, badges, card titles (`h3`), form labels, chips  |
| `--fw-bold`        | 700    | Page/section headings (`h2`), KPI / gauge / metric values, exit codes    |
| `--fw-extrabold`   | 800    | Brand wordmark (`h1`, drawer title) and the largest hero metrics         |

Rationale for the split:

- **800 is rationed.** It marks the very top of the hierarchy — the product
  wordmark and the single biggest dashboard/bandwidth numbers — so the brand and
  the headline metric read as premium and deliberate, not loud.
- **700** is the working "this is important" weight: every section heading and
  every large numeric readout (KPIs, gauges, traffic, forecasts, exit codes).
- **600** is the structural-label weight: anything that names or tags a thing —
  table column headers, badges/status chips, card titles, form field labels,
  uppercase eyebrows, active nav.
- **500** keeps interactive controls and secondary metadata a notch above body
  so they feel tappable/clickable without competing with headings.
- **400** is body. Exo 2 Regular is slightly lighter than Inter on dark, so the
  base sets `-webkit-font-smoothing: antialiased` for crisp light-on-dark text.

## Numerics & readability

Monitoring is the product's first priority, so numbers must line up. Metric
values, gauges, tables and other tabular data use **`font-variant-numeric:
tabular-nums`** (via a `--num` helper) so digits share a fixed advance width and
columns don't jitter as values change in live views. Uppercase micro-labels keep
their positive letter-spacing; large headings get a touch of negative tracking
to stay tight at display sizes.

## Where it lives

- `@font-face`, the `:root` family + weight/`--num` tokens, and the base rules
  are at the top of `web/static/style.css` (the global stylesheet loaded and
  precached on every page).
- Per-module stylesheets (`analytics.css`, `monitoring.css`, `nodes.css`, …)
  inherit the family and reference the same weight scale — no module declares
  its own `font-family` except the intentional monospace stacks.

When adding UI, reach for the weight tokens and the table above instead of
hardcoding a `font-weight`, and use `tabular-nums` for anything that displays
numbers in a column.
