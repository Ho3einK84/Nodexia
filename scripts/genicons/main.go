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
