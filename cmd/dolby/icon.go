package main

import "math"

// Tray icon colours (0xRRGGBB).
const (
	colorOff     = 0x3B82F6 // blue
	colorOn      = 0x22C55E // green
	colorUnknown = 0x9CA3AF // grey
)

// headphonePixels draws a simplified pair of headphones in one colour, as
// size×size pixels of 0xAARRGGBB, top row first. With outline set, only a
// rim along the shape's edge is drawn, which marks the tray as busy.
//
// The shape is laid out on a 16×16 grid and scaled, so it is drawn at the
// exact size the tray asks for. Each pixel's alpha is how far it lies inside
// the shape's edge, which anti-aliases the edges without any image files.
func headphonePixels(size int, rgb uint32, outline bool) []uint32 {
	px := make([]uint32, size*size)
	scale := float64(size) / 16
	rim := math.Max(1, float64(size)/14) // outline width in pixels
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := headphoneDist((float64(x)+0.5)/scale, (float64(y)+0.5)/scale) * scale
			cov := clamp01(0.5 - d)
			if outline {
				cov *= clamp01(d + rim + 0.5)
			}
			px[y*size+x] = uint32(math.Round(cov*255))<<24 | rgb
		}
	}
	return px
}

// headphoneDist is the signed distance, in grid units, from (x, y) on the
// 16×16 grid to the headphone outline: negative inside, positive outside.
// The shape is a headband (the upper half of a ring) joined to two ear cups
// (rounded rectangles).
func headphoneDist(x, y float64) float64 {
	const (
		cx, cy = 8.0, 8.5 // headband centre
		radius = 6.0      // headband centre line
		band   = 0.9      // half the headband's thickness
	)
	var dBand float64
	if y <= cy {
		dBand = math.Abs(math.Hypot(x-cx, y-cy)-radius) - band
	} else {
		// Below the centre the band ends in round caps, hidden in the cups.
		dBand = math.Min(math.Hypot(x-(cx-radius), y-cy), math.Hypot(x-(cx+radius), y-cy)) - band
	}
	left := roundedRectDist(x, y, 3.25, 11.5, 1.75, 3, 1.2)
	right := roundedRectDist(x, y, 12.75, 11.5, 1.75, 3, 1.2)
	return math.Min(dBand, math.Min(left, right))
}

// roundedRectDist is the signed distance from (x, y) to a rectangle centred
// at (cx, cy) with half-width hw, half-height hh and corner radius r.
func roundedRectDist(x, y, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(x-cx) - hw + r
	qy := math.Abs(y-cy) - hh + r
	outside := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
	inside := math.Min(math.Max(qx, qy), 0)
	return outside + inside - r
}

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}
