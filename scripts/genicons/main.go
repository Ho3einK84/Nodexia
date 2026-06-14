//go:build ignore

// Command genicons renders Nodexia's PWA icon set into web/static using only the
// Go standard library (no image toolchain required in CI). Regenerate after a
// brand change with:
//
//	go run scripts/genicons/main.go
//
// The motif is a small "node graph": a central hub linked to three satellites,
// echoing the product (monitoring and managing nodes). Shapes are rendered at 4x
// supersampling and box-downsampled for clean anti-aliasing.
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const supersample = 4

type rgb struct{ r, g, b float64 }

var (
	bgTop     = rgb{30.0 / 255, 41.0 / 255, 59.0 / 255}    // #1e293b
	bgBottom  = rgb{11.0 / 255, 17.0 / 255, 32.0 / 255}    // #0b1120
	hub       = rgb{59.0 / 255, 130.0 / 255, 246.0 / 255}  // #3b82f6 accent
	satellite = rgb{96.0 / 255, 165.0 / 255, 250.0 / 255}  // #60a5fa accent-soft
	link      = rgb{147.0 / 255, 197.0 / 255, 253.0 / 255} // #93c5fd accent-bright
)

type vec struct{ x, y float64 }

func main() {
	outDir := filepath.Join("web", "static")

	write(filepath.Join(outDir, "icon-192.png"), render(192, 0.66, true, false))
	write(filepath.Join(outDir, "icon-512.png"), render(512, 0.66, true, false))
	write(filepath.Join(outDir, "icon-maskable-512.png"), render(512, 0.62, false, false))
	write(filepath.Join(outDir, "apple-touch-icon.png"), render(180, 0.62, false, true))

	// Per-shortcut icons for the manifest "shortcuts" entries. Android renders a
	// blank placeholder when a shortcut declares no icon, so each long-press menu
	// entry gets its own recognizable 96x96 glyph (the Android baseline size) on
	// the shared branded background: a stacked list for Servers, a heartbeat for
	// Diagnostics, and an exclamation mark for Alerts.
	write(filepath.Join(outDir, "shortcut-servers.png"), renderShortcut(96, glyphServers))
	write(filepath.Join(outDir, "shortcut-diagnostics.png"), renderShortcut(96, glyphPulse))
	write(filepath.Join(outDir, "shortcut-alerts.png"), renderShortcut(96, glyphAlert))
}

// render draws an icon of the given size. contentFraction is the motif diameter
// as a fraction of the canvas; rounded clips the canvas to a rounded square with
// transparent corners; opaque forces a fully opaque square (for iOS, which
// applies its own mask). maskable icons use a full-bleed square background.
func render(size int, contentFraction float64, rounded, opaque bool) *image.NRGBA {
	ss := size * supersample
	hi := image.NewNRGBA(image.Rect(0, 0, ss, ss))

	fss := float64(ss)
	center := vec{fss / 2, fss / 2}

	// Corner radius for the rounded variant (in supersampled pixels).
	cornerR := 0.22 * fss

	// Motif geometry, in supersampled pixels. The motif's max extent (satellite
	// center distance + satellite radius) is normalised to 1.0, then scaled so
	// the motif diameter equals contentFraction of the canvas.
	const (
		satDist   = 0.78 // satellite distance from center, in motif units
		satRadius = 0.20
		hubRadius = 0.27
		linkHalfW = 0.038
	)
	motifR := (contentFraction * fss / 2) / (satDist + satRadius)

	type disk struct {
		c vec
		r float64
		k rgb
	}
	hubC := center
	sats := make([]vec, 3)
	for i := 0; i < 3; i++ {
		ang := -math.Pi/2 + float64(i)*(2*math.Pi/3) // up, lower-right, lower-left
		sats[i] = vec{
			center.x + motifR*satDist*math.Cos(ang),
			center.y + motifR*satDist*math.Sin(ang),
		}
	}
	disks := []disk{{hubC, motifR * hubRadius, hub}}
	for _, s := range sats {
		disks = append(disks, disk{s, motifR * satRadius, satellite})
	}

	for y := 0; y < ss; y++ {
		// Vertical background gradient.
		t := float64(y) / fss
		bg := mix(bgTop, bgBottom, t)
		for x := 0; x < ss; x++ {
			p := vec{float64(x) + 0.5, float64(y) + 0.5}

			// Base alpha: rounded clips corners; otherwise full square.
			alpha := 1.0
			if rounded && !opaque {
				alpha = roundedRectCoverage(p, fss, cornerR)
				if alpha <= 0 {
					hi.Set(x, y, color.NRGBA{})
					continue
				}
			}

			col := bg

			// Links (drawn under nodes) from hub to each satellite.
			for _, s := range sats {
				cov := capsuleCoverage(p, hubC, s, motifR*linkHalfW)
				if cov > 0 {
					col = over(link, col, cov*0.9)
				}
			}
			// Nodes.
			for _, d := range disks {
				cov := diskCoverage(p, d.c, d.r)
				if cov > 0 {
					col = over(d.k, col, cov)
				}
			}

			hi.Set(x, y, toNRGBA(col, alpha))
		}
	}

	return downsample(hi, size)
}

// glyphFunc returns anti-aliased coverage of a shortcut glyph at point p, given
// the icon center c and the glyph half-extent R (both in supersampled pixels).
type glyphFunc func(p, c vec, R float64) float64

// renderShortcut draws a 96x96-class shortcut icon: the shared branded rounded
// background (matching the rounded app icons) with a single bright glyph on top.
func renderShortcut(size int, draw glyphFunc) *image.NRGBA {
	ss := size * supersample
	hi := image.NewNRGBA(image.Rect(0, 0, ss, ss))

	fss := float64(ss)
	center := vec{fss / 2, fss / 2}
	cornerR := 0.22 * fss
	glyphR := 0.30 * fss
	glyphCol := rgb{241.0 / 255, 245.0 / 255, 249.0 / 255} // #f1f5f9 slate-100

	for y := 0; y < ss; y++ {
		t := float64(y) / fss
		bg := mix(bgTop, bgBottom, t)
		for x := 0; x < ss; x++ {
			p := vec{float64(x) + 0.5, float64(y) + 0.5}

			alpha := roundedRectCoverage(p, fss, cornerR)
			if alpha <= 0 {
				hi.Set(x, y, color.NRGBA{})
				continue
			}

			col := bg
			if cov := draw(p, center, glyphR); cov > 0 {
				col = over(glyphCol, col, cov)
			}
			hi.Set(x, y, toNRGBA(col, alpha))
		}
	}

	return downsample(hi, size)
}

// glyphServers draws three stacked rounded bars — a server/host list.
func glyphServers(p, c vec, R float64) float64 {
	halfW := R * 0.82
	halfH := R * 0.15
	gap := R * 0.6
	cov := 0.0
	for i := -1; i <= 1; i++ {
		y := c.y + float64(i)*gap
		a := vec{c.x - halfW + halfH, y}
		b := vec{c.x + halfW - halfH, y}
		cov = math.Max(cov, capsuleCoverage(p, a, b, halfH))
	}
	return cov
}

// glyphPulse draws an ECG-style heartbeat line — diagnostics/health.
func glyphPulse(p, c vec, R float64) float64 {
	half := R * 0.13
	pts := []vec{
		{c.x - R, c.y},
		{c.x - R*0.45, c.y},
		{c.x - R*0.2, c.y - R*0.72},
		{c.x + R*0.05, c.y + R*0.72},
		{c.x + R*0.35, c.y},
		{c.x + R, c.y},
	}
	cov := 0.0
	for i := 0; i+1 < len(pts); i++ {
		cov = math.Max(cov, capsuleCoverage(p, pts[i], pts[i+1], half))
	}
	return cov
}

// glyphAlert draws an exclamation mark — alerts/warnings.
func glyphAlert(p, c vec, R float64) float64 {
	half := R * 0.16
	top := vec{c.x, c.y - R*0.82}
	bot := vec{c.x, c.y + R*0.22}
	stem := capsuleCoverage(p, top, bot, half)
	dot := diskCoverage(p, vec{c.x, c.y + R*0.72}, half*1.1)
	return math.Max(stem, dot)
}

// diskCoverage returns 1-pixel anti-aliased coverage of a filled disk.
func diskCoverage(p, c vec, r float64) float64 {
	d := math.Hypot(p.x-c.x, p.y-c.y)
	return clamp01(r - d + 0.5)
}

// capsuleCoverage returns coverage of a thick line segment (rounded caps).
func capsuleCoverage(p, a, b vec, halfW float64) float64 {
	abx, aby := b.x-a.x, b.y-a.y
	apx, apy := p.x-a.x, p.y-a.y
	denom := abx*abx + aby*aby
	t := 0.0
	if denom > 0 {
		t = clamp01((apx*abx + apy*aby) / denom)
	}
	cx, cy := a.x+t*abx, a.y+t*aby
	d := math.Hypot(p.x-cx, p.y-cy)
	return clamp01(halfW - d + 0.5)
}

// roundedRectCoverage returns coverage of a rounded square filling the canvas.
func roundedRectCoverage(p vec, size, radius float64) float64 {
	// Distance to the rounded-rect boundary via the standard SDF.
	hx := size/2 - radius
	hy := size/2 - radius
	qx := math.Abs(p.x-size/2) - hx
	qy := math.Abs(p.y-size/2) - hy
	dx := math.Max(qx, 0)
	dy := math.Max(qy, 0)
	outside := math.Hypot(dx, dy)
	inside := math.Min(math.Max(qx, qy), 0)
	d := outside + inside - radius
	return clamp01(-d + 0.5)
}

func downsample(src *image.NRGBA, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	n := supersample
	inv := 1.0 / float64(n*n)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					c := src.NRGBAAt(x*n+sx, y*n+sy)
					af := float64(c.A) / 255
					// Average in straight-alpha then re-associate; premultiply for
					// correct edge blending against transparent corners.
					r += float64(c.R) * af
					g += float64(c.G) * af
					b += float64(c.B) * af
					a += af
				}
			}
			a *= inv
			r *= inv
			g *= inv
			b *= inv
			out := color.NRGBA{A: uint8(math.Round(a * 255))}
			if a > 0 {
				out.R = uint8(math.Round(clampByte(r / a)))
				out.G = uint8(math.Round(clampByte(g / a)))
				out.B = uint8(math.Round(clampByte(b / a)))
			}
			dst.SetNRGBA(x, y, out)
		}
	}
	return dst
}

func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// over composites src over dst with the given source coverage (straight alpha).
func over(src, dst rgb, cov float64) rgb {
	return rgb{
		src.r*cov + dst.r*(1-cov),
		src.g*cov + dst.g*(1-cov),
		src.b*cov + dst.b*(1-cov),
	}
}

func toNRGBA(c rgb, alpha float64) color.NRGBA {
	return color.NRGBA{
		R: uint8(math.Round(clampByte(c.r * 255))),
		G: uint8(math.Round(clampByte(c.g * 255))),
		B: uint8(math.Round(clampByte(c.b * 255))),
		A: uint8(math.Round(clamp01(alpha) * 255)),
	}
}

func clamp01(v float64) float64   { return math.Max(0, math.Min(1, v)) }
func clampByte(v float64) float64 { return math.Max(0, math.Min(255, v)) }

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
