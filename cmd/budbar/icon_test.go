package main

import "testing"

func alpha(p uint32) uint32 { return p >> 24 }

func TestHeadphonePixels(t *testing.T) {
	for _, size := range []int{16, 20, 24, 32} {
		px := headphonePixels(size, colorOn, false)
		if len(px) != size*size {
			t.Fatalf("size %d: %d pixels", size, len(px))
		}
		at := func(gx, gy float64) uint32 { // a point on the 16×16 design grid
			return px[int(gy*float64(size)/16)*size+int(gx*float64(size)/16)]
		}
		for _, p := range px {
			if p&0xFFFFFF != colorOn {
				t.Fatalf("size %d: pixel %08x is not the icon colour", size, p)
			}
		}
		if alpha(at(8, 2.5)) != 255 || alpha(at(3.25, 12)) != 255 || alpha(at(12.75, 12)) != 255 {
			t.Errorf("size %d: headband or cups not solid", size)
		}
		if alpha(at(8, 10)) != 0 || alpha(at(0.2, 0.2)) != 0 || alpha(at(8, 15.5)) != 0 {
			t.Errorf("size %d: background not transparent", size)
		}
	}
}

func TestHeadphoneOutlineIsHollow(t *testing.T) {
	for _, size := range []int{16, 24} {
		filled := headphonePixels(size, colorOff, false)
		outline := headphonePixels(size, colorOff, true)
		cup := (size*12/16)*size + size*13/64 // centre of the left cup
		if alpha(filled[cup]) != 255 || alpha(outline[cup]) > 64 {
			t.Errorf("size %d: cup centre alpha filled %d, outline %d", size, alpha(filled[cup]), alpha(outline[cup]))
		}
		for i := range outline {
			if alpha(outline[i]) > alpha(filled[i]) {
				t.Fatalf("size %d: outline draws outside the shape at pixel %d", size, i)
			}
		}
	}
}
