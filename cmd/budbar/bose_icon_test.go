package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func channels(p uint32) (a, r, g, b int) {
	return int(p >> 24), int(p >> 16 & 0xFF), int(p >> 8 & 0xFF), int(p & 0xFF)
}

func TestBosePixels(t *testing.T) {
	for _, size := range []int{16, 20, 24, 32, 48} {
		px, err := bosePixels(size, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(px) != size*size {
			t.Fatalf("size %d: %d pixels", size, len(px))
		}
		at := func(fx, fy float64) uint32 { // a point as a fraction of the icon
			return px[int(fy*float64(size))*size+int(fx*float64(size))]
		}
		// Corners are background; the front cup is solid; the space under the
		// headband is see-through.
		for _, c := range []uint32{at(0, 0), at(0.99, 0), at(0, 0.99), at(0.99, 0.99), at(0.38, 0.34)} {
			if a, _, _, _ := channels(c); a > 16 {
				t.Errorf("size %d: background pixel %08x not transparent", size, c)
			}
		}
		if a, _, _, _ := channels(at(0.4, 0.75)); a < 240 {
			t.Errorf("size %d: front cup alpha %d", size, a)
		}
		// The gold yoke survives at every size: some opaque pixel where red
		// clearly exceeds blue.
		gold := false
		for _, p := range px {
			a, r, _, b := channels(p)
			if a > 200 && r > 150 && r-b > 60 {
				gold = true
				break
			}
		}
		if !gold {
			t.Errorf("size %d: no gold pixel", size)
		}
	}
}

// meanOpaque is the mean brightness and saturation of the mostly opaque
// pixels, and their count.
func meanOpaque(px []uint32) (bright, sat float64, n int) {
	for _, p := range px {
		a, r, g, b := channels(p)
		if a < 128 {
			continue
		}
		hi, lo := max(r, g, b), min(r, g, b)
		bright += float64(r+g+b) / 3
		sat += float64(hi - lo)
		n++
	}
	return bright / float64(n), sat / float64(n), n
}

func TestBoseDarkIsLighter(t *testing.T) {
	light, _ := bosePixels(24, false, false)
	dark, _ := bosePixels(24, true, false)
	bl, _, _ := meanOpaque(light)
	bd, _, _ := meanOpaque(dark)
	if bd < bl+40 {
		t.Errorf("dark-taskbar icon brightness %.0f, light-taskbar %.0f", bd, bl)
	}
	for i := range light {
		if light[i]>>24 != dark[i]>>24 {
			t.Fatalf("pixel %d: lifting changed alpha", i)
		}
	}
}

func TestBoseDimIsGreyAndFaint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		on, _ := bosePixels(24, dark, false)
		dim, _ := bosePixels(24, dark, true)
		_, son, _ := meanOpaque(on)
		var aOn, aDim int
		for i := range on {
			aOn += int(on[i] >> 24)
			aDim += int(dim[i] >> 24)
		}
		if float64(aDim) > float64(aOn)*0.6 {
			t.Errorf("dark=%v: dim alpha sum %d, normal %d", dark, aDim, aOn)
		}
		// Dim pixels are all below 128 alpha, so compare saturation directly.
		var sdim float64
		n := 0
		for _, p := range dim {
			a, r, g, b := channels(p)
			if a > 64 {
				sdim += float64(max(r, g, b) - min(r, g, b))
				n++
			}
		}
		if sdim/float64(n) > son*0.5 {
			t.Errorf("dark=%v: dim saturation %.1f, normal %.1f", dark, sdim/float64(n), son)
		}
	}
}

// TestBoseIconPreview writes the icon in all four looks at tray sizes, on a
// light and a dark taskbar colour, to the PNG named by BUDBAR_ICON_PREVIEW.
func TestBoseIconPreview(t *testing.T) {
	path := os.Getenv("BUDBAR_ICON_PREVIEW")
	if path == "" {
		t.Skip("BUDBAR_ICON_PREVIEW not set")
	}
	sizes := []int{16, 24, 32}
	const zoom = 4
	cell := 32*zoom + 8
	bgs := []color.NRGBA{{0xF3, 0xF3, 0xF3, 255}, {0x20, 0x20, 0x20, 255}}
	img := image.NewNRGBA(image.Rect(0, 0, cell*len(sizes)*2, cell*2))
	for row, bg := range bgs {
		dark := row == 1
		for col := 0; col < len(sizes)*2; col++ {
			size, dim := sizes[col%len(sizes)], col >= len(sizes)
			px, err := bosePixels(size, dark, dim)
			if err != nil {
				t.Fatal(err)
			}
			z := zoom * 32 / size
			for y := 0; y < cell; y++ {
				for x := 0; x < cell; x++ {
					c := bg
					if x >= 4 && y >= 4 && x < 4+size*z && y < 4+size*z {
						a, r, g, b := channels(px[(y-4)/z*size+(x-4)/z])
						m := func(f int, b uint8) uint8 { return uint8((f*a + int(b)*(255-a)) / 255) }
						c = color.NRGBA{m(r, bg.R), m(g, bg.G), m(b, bg.B), 255}
					}
					img.SetNRGBA(col*cell+x, row*cell+y, c)
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
