package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"math"
)

// bosePNG is the Bose headphones drawing: 256×256, transparent background.
//
//go:embed bose.png
var bosePNG []byte

// Look of the Bose icon on a dark taskbar: every colour channel c becomes
// liftBase + (1-liftBase)*c, which raises the black band and dark cups to grey
// while the gold yoke changes little.
const liftBase = 0.42

// A dimmed icon (the last check failed) is mostly grey and half transparent.
const (
	dimGrey  = 0.7  // how far colours move toward their own grey level
	dimAlpha = 0.55 // alpha multiplier
)

// bosePixels draws the Bose icon at size×size pixels of 0xAARRGGBB, top row
// first, with straight (not premultiplied) alpha. Each pixel averages the
// drawing's pixels it covers, weighted by their alpha, so the icon is drawn
// at the size the tray asks for. dark lightens it for a dark taskbar; dim
// marks a failed check.
func bosePixels(size int, dark, dim bool) ([]uint32, error) {
	img, err := png.Decode(bytes.NewReader(bosePNG))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	px := make([]uint32, size*size)
	for oy := 0; oy < size; oy++ {
		for ox := 0; ox < size; ox++ {
			var r, g, bl, a float64
			n := 0
			for y := oy * h / size; y < (oy+1)*h/size; y++ {
				for x := ox * w / size; x < (ox+1)*w/size; x++ {
					pr, pg, pb, pa := straight(img, b.Min.X+x, b.Min.Y+y)
					r += pr * pa
					g += pg * pa
					bl += pb * pa
					a += pa
					n++
				}
			}
			if a == 0 {
				continue
			}
			r, g, bl, a = r/a, g/a, bl/a, a/float64(n)
			if dark {
				r, g, bl = lift(r), lift(g), lift(bl)
			}
			if dim {
				grey := 0.299*r + 0.587*g + 0.114*bl
				r, g, bl = mix(r, grey), mix(g, grey), mix(bl, grey)
				a *= dimAlpha
			}
			px[oy*size+ox] = byte255(a)<<24 | byte255(r)<<16 | byte255(g)<<8 | byte255(bl)
		}
	}
	return px, nil
}

// straight returns the colour at (x, y) as straight-alpha values in [0, 1].
// image.Image.At().RGBA() is premultiplied, so colour is divided by alpha.
func straight(img image.Image, x, y int) (r, g, b, a float64) {
	pr, pg, pb, pa := img.At(x, y).RGBA()
	if pa == 0 {
		return 0, 0, 0, 0
	}
	return float64(pr) / float64(pa), float64(pg) / float64(pa), float64(pb) / float64(pa), float64(pa) / 0xFFFF
}

func lift(c float64) float64      { return liftBase + (1-liftBase)*c }
func mix(c, grey float64) float64 { return c + (grey-c)*dimGrey }

func byte255(v float64) uint32 { return uint32(math.Round(clamp01(v) * 255)) }
