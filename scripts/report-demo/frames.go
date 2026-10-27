package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"strings"
	"time"
)

// Synthetic "screenshots" for the demo report: a mock application window drawn
// in code, labelled with the app name and time. No user data is read.

var glyphs = map[rune][7]string{
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C': {"01111", "10000", "10000", "10000", "10000", "10000", "01111"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01111", "10000", "10000", "10011", "10001", "10001", "01111"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"11111", "00100", "00100", "00100", "00100", "00100", "11111"},
	'J': {"00111", "00010", "00010", "00010", "00010", "10010", "01100"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "10101", "01010"},
	'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"},
	'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "11110", "00001", "00001", "10001", "01110"},
	'6': {"00110", "01000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00010", "01100"},
	':': {"00000", "00100", "00100", "00000", "00100", "00100", "00000"},
	'-': {"00000", "00000", "00000", "11111", "00000", "00000", "00000"},
	'.': {"00000", "00000", "00000", "00000", "00000", "01100", "01100"},
	'·': {"00000", "00000", "00000", "01100", "01100", "00000", "00000"},
}

func drawText(img *image.RGBA, x, y, scale int, text string, c color.RGBA) {
	for _, r := range strings.ToUpper(text) {
		if g, ok := glyphs[r]; ok {
			for row, line := range g {
				for col, bit := range line {
					if bit == '1' {
						fill(img, x+col*scale, y+row*scale, scale, scale, c)
					}
				}
			}
		}
		x += 6 * scale
	}
}

func fill(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
}

type mockApp struct {
	label  string
	accent color.RGBA
	dark   bool
}

var mockApps = map[string]mockApp{
	"com.microsoft.VSCode":  {"VS CODE", color.RGBA{0x3b, 0x82, 0xd6, 255}, true},
	"com.google.Chrome":     {"CHROME", color.RGBA{0xd0, 0x87, 0x3a, 255}, false},
	"ru.keepcoder.Telegram": {"TELEGRAM", color.RGBA{0x2f, 0x9e, 0xc8, 255}, false},
	"com.apple.Notes":       {"NOTES", color.RGBA{0xe0, 0xb0, 0x30, 255}, false},
	"com.apple.iCal":        {"CALENDAR", color.RGBA{0xc2, 0x5f, 0x5f, 255}, false},
	"com.apple.finder":      {"FINDER", color.RGBA{0x5f, 0x8f, 0xd8, 255}, false},
}

// mockFrame renders a 1100x700 JPEG that looks like a window of the given app.
func mockFrame(at time.Time, bundle string, seed int) []byte {
	app, ok := mockApps[bundle]
	if !ok {
		app = mockApp{"DEMO", color.RGBA{0x4f, 0x5b, 0xd5, 255}, false}
	}
	const w, h = 1100, 700
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ { // desktop backdrop
		fill(img, 0, y, w, 1, color.RGBA{uint8(70 + y/14), uint8(80 + y/12), uint8(110 + y/10), 255})
	}
	bg, line, side := color.RGBA{250, 250, 252, 255}, color.RGBA{214, 218, 226, 255}, color.RGBA{238, 240, 245, 255}
	if app.dark {
		bg, line, side = color.RGBA{30, 32, 38, 255}, color.RGBA{70, 76, 90, 255}, color.RGBA{40, 43, 51, 255}
	}
	fill(img, 40, 30, w-80, h-60, bg)
	fill(img, 40, 30, w-80, 44, color.RGBA{225, 228, 234, 255})
	for i, c := range []color.RGBA{{236, 95, 88, 255}, {244, 190, 79, 255}, {98, 197, 85, 255}} {
		fill(img, 58+i*22, 46, 12, 12, c)
	}
	drawText(img, 140, 41, 3, app.label+" · DEMO "+at.Format("15:04:05"), color.RGBA{60, 64, 74, 255})
	fill(img, 40, 74, 190, h-104, side)
	fill(img, 60, 100, 150, 26, app.accent)
	for i := 0; i < 9; i++ {
		fill(img, 60, 146+i*30, 80+(seed*13+i*29)%70, 12, line)
	}
	for i := 0; i < 15; i++ {
		width := 180 + (seed*37+i*53)%560
		c := line
		if (seed+i)%5 == 0 {
			c = app.accent
		}
		fill(img, 260+(i%3)*24, 100+i*34, width, 14, c)
	}
	drawText(img, 260, h-80, 4, "SYNTHETIC SAMPLE", app.accent)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 80}); err != nil {
		panic(err)
	}
	return out.Bytes()
}
