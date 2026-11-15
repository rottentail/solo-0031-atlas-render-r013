package raster

import (
	"atlas-render/internal/codec"
	"atlas-render/internal/geometry"
	"atlas-render/internal/model"
	"atlas-render/internal/paint"
	"atlas-render/internal/scene"
	"image"
	"image/color"
	"math"
)

// maxWork bounds the total layer pixels one compiled plan may touch.
const maxWork = 40_000_000

// clip is one resolved ancestor clipping rectangle.
type clip struct {
	inverse geometry.Matrix
	rect    model.Rect
}

// operation is a fully resolved draw call. Containment and color sampling
// are settled at compile time and captured as immutable values, so execution
// never re-reads the scene document.
type operation struct {
	inverse  geometry.Matrix
	clips    []clip
	opacity  float64
	bounds   image.Rectangle
	contains func(local model.Point) bool
	sample   func(local model.Point) color.NRGBA
}

// Plan is the validated, immutable result of compiling a pinned snapshot.
// It carries no references into the scene document, is safe to read and
// render from concurrent goroutines, and Render never mutates it.
type Plan struct {
	width      int
	height     int
	scale      int
	background color.NRGBA
	ops        []operation
	cost       int64
}

// Width and Height are the output dimensions in pixels.
func (p Plan) Width() int  { return p.width }
func (p Plan) Height() int { return p.height }

// Cost is the measured layer-pixel work the plan was admitted under.
func (p Plan) Cost() int64 { return p.cost }

// Compile validates the snapshot document and resolves it into an immutable
// execution plan. It is the single admission and execution criterion: job
// submission and the worker both compile the pinned snapshot through this
// function, so the enqueue budget and the actual render can never drift into
// separate judgments.
func Compile(s model.Snapshot, scale int) (Plan, error) {
	if e := scene.Validate(s.Document); e != nil {
		return Plan{}, e
	}
	if scale != 1 && scale != 2 {
		return Plan{}, model.Invalid("scale must be one or two")
	}
	bg, _ := paint.Parse(s.Document.Background) // validated above
	p := Plan{width: s.Document.Width * scale, height: s.Document.Height * scale, scale: scale, background: bg}
	decoded := map[string]image.Image{}
	var walk func([]model.Layer, geometry.Matrix, []clip, float64) error
	walk = func(ls []model.Layer, parent geometry.Matrix, clips []clip, alpha float64) error {
		for _, l := range ls {
			world := parent.Mul(geometry.From(l.Transform))
			inv, ok := world.Inverse()
			if !ok {
				return model.Invalid("layer %s has singular accumulated transform", l.ID)
			}
			localClips := append([]clip(nil), clips...)
			if l.Clip != nil {
				localClips = append(localClips, clip{inverse: inv, rect: *l.Clip})
			}
			a := alpha * l.Opacity
			if l.Kind == "group" {
				if e := walk(l.Children, world, localClips, a); e != nil {
					return e
				}
				continue
			}
			corners := []model.Point{{}, {X: l.Width}, {Y: l.Height}, {X: l.Width, Y: l.Height}}
			minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
			for _, c := range corners {
				v := world.Apply(c)
				minX = math.Min(minX, v.X)
				minY = math.Min(minY, v.Y)
				maxX = math.Max(maxX, v.X)
				maxY = math.Max(maxY, v.Y)
			}
			minX = math.Max(0, math.Min(float64(s.Document.Width), minX))
			maxX = math.Max(0, math.Min(float64(s.Document.Width), maxX))
			minY = math.Max(0, math.Min(float64(s.Document.Height), minY))
			maxY = math.Max(0, math.Min(float64(s.Document.Height), maxY))
			b := image.Rect(int(math.Floor(minX*float64(scale))), int(math.Floor(minY*float64(scale))), int(math.Ceil(maxX*float64(scale))), int(math.Ceil(maxY*float64(scale))))
			op := operation{inverse: inv, clips: localClips, opacity: a, bounds: b, contains: containment(l)}
			if l.Kind == "image" {
				asset, ok := s.Assets[l.AssetID]
				if !ok {
					return model.Invalid("missing pinned asset %s", l.AssetID)
				}
				img := decoded[l.AssetID]
				if img == nil {
					var e error
					img, e = codec.Decode(asset.PNG)
					if e != nil {
						return e
					}
					decoded[l.AssetID] = img
				}
				op.sample = bitmapSampler(img, l.Width, l.Height)
			} else {
				op.sample = paint.Compile(l.Fill)
			}
			p.cost += int64(b.Dx()) * int64(b.Dy())
			if p.cost > maxWork {
				return model.Busy("scene exceeds raster work budget")
			}
			p.ops = append(p.ops, op)
		}
		return nil
	}
	if e := walk(s.Document.Layers, geometry.Identity(), nil, 1); e != nil {
		return Plan{}, e
	}
	return p, nil
}

// containment resolves the layer's local-space hit test once. Every case
// keeps the half-open local rectangle and adds the kind-specific test;
// polygon vertices are copied so the plan cannot alias the document.
func containment(l model.Layer) func(model.Point) bool {
	rect := model.Rect{Width: l.Width, Height: l.Height}
	switch l.Kind {
	case "ellipse":
		w, h := l.Width, l.Height
		return func(local model.Point) bool {
			if !geometry.InRect(local, rect) {
				return false
			}
			dx, dy := 2*local.X/w-1, 2*local.Y/h-1
			return dx*dx+dy*dy <= 1
		}
	case "polygon":
		points := append([]model.Point(nil), l.Points...)
		return func(local model.Point) bool {
			return geometry.InRect(local, rect) && geometry.InPolygon(local, points)
		}
	default: // rectangle, image
		return func(local model.Point) bool {
			return geometry.InRect(local, rect)
		}
	}
}

// bitmapSampler resolves nearest-neighbor reads against the decoded asset.
func bitmapSampler(img image.Image, w, h float64) func(model.Point) color.NRGBA {
	b := img.Bounds()
	minX, minY, dx, dy := b.Min.X, b.Min.Y, float64(b.Dx()), float64(b.Dy())
	return func(local model.Point) color.NRGBA {
		sx := minX + int(local.X/w*dx)
		sy := minY + int(local.Y/h*dy)
		return color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
	}
}
