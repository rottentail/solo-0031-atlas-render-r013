package paint

import (
	"atlas-render/internal/model"
	"fmt"
	"image/color"
	"math"
	"strconv"
)

func Parse(s string) (color.NRGBA, error) {
	if len(s) != 7 || s[0] != '#' {
		return color.NRGBA{}, fmt.Errorf("color must be #RRGGBB")
	}
	n, e := strconv.ParseUint(s[1:], 16, 24)
	if e != nil {
		return color.NRGBA{}, e
	}
	return color.NRGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, nil
}

// Sampler maps a local layer coordinate to a color.
type Sampler func(at model.Point) color.NRGBA

// Compile resolves a validated paint into a sampler, parsing colors and
// gradient geometry once instead of on every pixel. The paint must already
// pass scene validation; invalid colors keep the zero-value fallback that
// per-pixel parsing produced.
func Compile(p model.Paint) Sampler {
	a, _ := Parse(p.Color)
	if p.Kind != "linear" {
		return func(model.Point) color.NRGBA { return a }
	}
	b, _ := Parse(p.EndColor)
	dx, dy := p.To.X-p.From.X, p.To.Y-p.From.Y
	den := dx*dx + dy*dy
	return func(at model.Point) color.NRGBA {
		t := 0.0
		if den > 0 {
			t = ((at.X-p.From.X)*dx + (at.Y-p.From.Y)*dy) / den
		}
		t = math.Max(0, math.Min(1, t))
		mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*(1-t) + float64(y)*t)) }
		return color.NRGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 255}
	}
}
