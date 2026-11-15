package raster

import (
	"atlas-render/internal/geometry"
	"atlas-render/internal/model"
	"context"
	"image"
	"image/color"
	"image/draw"
	"math"
)

// Render executes a compiled plan. It consumes only the precomputed
// operations — the scene document is never re-interpreted here — and it
// treats the plan as immutable: the in-progress image is private to the
// call, so cancellation discards it without leaving shared state behind.
func (p Plan) Render(ctx context.Context) (*image.NRGBA, error) {
	img := image.NewNRGBA(image.Rect(0, 0, p.width, p.height))
	draw.Draw(img, img.Bounds(), image.NewUniform(p.background), image.Point{}, draw.Src)
	for _, op := range p.ops {
		if op.opacity == 0 {
			continue
		}
		for y := op.bounds.Min.Y; y < op.bounds.Max.Y; y++ {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			for x := op.bounds.Min.X; x < op.bounds.Max.X; x++ {
				world := model.Point{X: (float64(x) + .5) / float64(p.scale), Y: (float64(y) + .5) / float64(p.scale)}
				local := op.inverse.Apply(world)
				if !op.contains(local) {
					continue
				}
				visible := true
				for _, c := range op.clips {
					if !geometry.InRect(c.inverse.Apply(world), c.rect) {
						visible = false
						break
					}
				}
				if !visible {
					continue
				}
				c := op.sample(local)
				c.A = uint8(math.Round(float64(c.A) * op.opacity))
				blend(img, x, y, c)
			}
		}
	}
	return img, nil
}
func blend(img *image.NRGBA, x, y int, s color.NRGBA) {
	d := img.NRGBAAt(x, y)
	sa, da := float64(s.A)/255, float64(d.A)/255
	a := sa + da*(1-sa)
	if a == 0 {
		img.SetNRGBA(x, y, color.NRGBA{})
		return
	}
	mix := func(u, v uint8) uint8 { return uint8(math.Round((float64(u)*sa + float64(v)*da*(1-sa)) / a)) }
	img.SetNRGBA(x, y, color.NRGBA{R: mix(s.R, d.R), G: mix(s.G, d.G), B: mix(s.B, d.B), A: uint8(math.Round(a * 255))})
}
