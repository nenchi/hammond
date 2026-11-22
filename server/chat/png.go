package chat

// the graph as a PNG: bars with the amount on top, labels below, title and totals in the picture
// (so a forwarded image still says what it is). Go fonts: no emoji, so the title's leading emoji is dropped.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var (
	pngBg    = color.RGBA{0xff, 0xff, 0xff, 0xff}
	pngText  = color.RGBA{0x1f, 0x29, 0x37, 0xff}
	pngMuted = color.RGBA{0x6b, 0x72, 0x80, 0xff}
	pngGrid  = color.RGBA{0xe5, 0xe7, 0xeb, 0xff}
	pngWhite = color.RGBA{0xff, 0xff, 0xff, 0xff}
	// stacked pieces alternate a dark and a light shade: fuel blue, other costs orange
	pngFuel  = [2]color.RGBA{{0x25, 0x63, 0xeb, 0xff}, {0x93, 0xc5, 0xfd, 0xff}}
	pngOther = [2]color.RGBA{{0xd9, 0x77, 0x06, 0xff}, {0xfc, 0xd3, 0x4d, 0xff}}
)

func face(ttf []byte, size float64) font.Face {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err) // embedded font: can't fail at run time
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	return fc
}

var faceTitle, faceText, faceSmall, faceTiny = face(gobold.TTF, 34), face(goregular.TTF, 24), face(goregular.TTF, 20), face(goregular.TTF, 16)

// text at x (left, or centered when center) with the baseline at y
func drawText(img draw.Image, f font.Face, c color.Color, s string, x, y int, center bool) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f}
	if center {
		x -= d.MeasureString(s).Round() / 2
	}
	d.Dot = fixed.P(x, y)
	d.DrawString(s)
}

func (g graphData) png() ([]byte, error) {
	const w, h, side, top, plotH = 900, 640, 30, 120, 360
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(pngBg), image.Point{}, draw.Src)
	title := g.title
	if _, t, ok := strings.Cut(title, " "); ok {
		title = t // "⛽ Fuel · 14 days" → "Fuel · 14 days"
	}
	drawText(img, faceTitle, pngText, title, side, 50, false)
	drawText(img, faceText, pngMuted, g.sub, side, 88, false)

	var max float64
	for _, b := range g.buckets {
		max = maxf(max, b.amount)
	}
	base := top + plotH
	draw.Draw(img, image.Rect(side, base, w-side, base+2), image.NewUniform(pngGrid), image.Point{}, draw.Src)
	n := len(g.buckets)
	slot := float64(w-2*side) / float64(n)
	// narrow bars (a year = 13 months): smaller labels, so every bar keeps its amount and date
	lf, labelW := faceSmall, 90.0
	if slot < 90 {
		lf, labelW = faceTiny, 58
	}
	every := int(math.Ceil(labelW / slot)) // show every n-th label only when they'd still overlap
	for i, b := range g.buckets {
		cx := side + int(slot*(float64(i)+0.5))
		if max > 0 && b.amount > 0 {
			bh := int(b.amount / max * (plotH - 40))
			bw := int(math.Max(2, slot*0.7))
			// one piece per fill-up / cost, oldest at the bottom, with its litres and € inside when it fits
			cum, nf, no := 0.0, 0, 0
			for j, p := range b.parts {
				y0 := base - int(cum/max*(plotH-40))
				cum += p.amount
				y1 := base - int(cum/max*(plotH-40))
				if j == len(b.parts)-1 {
					y1 = base - bh
				}
				shade, dark := pngFuel[nf%2], nf%2 == 0
				if p.qty > 0 {
					nf++
				} else {
					shade, dark = pngOther[no%2], no%2 == 0
					no++
				}
				draw.Draw(img, image.Rect(cx-bw/2, y1, cx+bw/2+bw%2, y0), image.NewUniform(shade), image.Point{}, draw.Src)
				if j > 0 { // a thin gap between pieces
					draw.Draw(img, image.Rect(cx-bw/2, y0-1, cx+bw/2+bw%2, y0+1), image.NewUniform(pngBg), image.Point{}, draw.Src)
				}
				tc := pngText
				if dark {
					tc = pngWhite
				}
				lines := []string{fmt.Sprintf("%.2f%s", p.amount, g.cur)}
				if p.qty > 0 {
					lines = []string{fmt.Sprintf("%.2f l", p.qty), lines[0]}
				}
				h := y0 - y1
				switch {
				case bw < 90:
				case h >= 50 && len(lines) == 2:
					drawText(img, faceSmall, tc, lines[0], cx, y1+(h-44)/2+18, true)
					drawText(img, faceSmall, tc, lines[1], cx, y1+(h-44)/2+40, true)
				case h >= 24:
					drawText(img, faceSmall, tc, lines[0], cx, y1+h/2+7, true)
				}
			}
			if slot >= 45 || b.amount == max || i == n-1 {
				val := fmt.Sprintf("%.2f%s", b.amount, g.cur) // € on top (exact when there's room), litres under the date
				if slot < 100 {
					val = fmt.Sprintf("%.0f%s", b.amount, g.cur)
				}
				drawText(img, lf, pngText, val, cx, base-bh-8, true)
			}
		}
		if i%every == 0 || (i == n-1 && (n-1)%every >= every/2+1) {
			drawText(img, lf, pngMuted, b.label, cx, base+28, true)
			if b.qty > 0 && slot >= 45 {
				l := fmt.Sprintf("%.2f l", b.qty)
				if slot < 100 {
					l = fmt.Sprintf("%.0f l", b.qty)
				}
				drawText(img, lf, pngMuted, l, cx, base+52, true)
			}
		}
	}
	for i, l := range g.foot {
		drawText(img, faceText, pngText, strings.ReplaceAll(l, "⌀", "avg"), side, base+100+i*34, false)
	}
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}
