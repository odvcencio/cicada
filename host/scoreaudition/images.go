package scoreaudition

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
)

var rampStops = [][3]float64{{0, 0, 4}, {40, 11, 84}, {101, 21, 110}, {159, 42, 99}, {212, 72, 66}, {245, 125, 21}, {250, 193, 39}, {252, 255, 164}}

func ramp(v float64) color.NRGBA {
	v = math.Max(0, math.Min(1, v)) * float64(len(rampStops)-1)
	i := int(v)
	if i > len(rampStops)-2 {
		i = len(rampStops) - 2
	}
	f := v - float64(i)
	var c [3]uint8
	for k := 0; k < 3; k++ {
		c[k] = uint8(rampStops[i][k]*(1-f) + rampStops[i+1][k]*f)
	}
	return color.NRGBA{c[0], c[1], c[2], 255}
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DefaultImageWidth caps the width of each time-axis picture, in pixels. An
// agent's image input is scaled to about 1,568 pixels on its long side, so a
// wider strip loses its bars and hits.
const DefaultImageWidth = 1600

// Picture is one image file that audition -o writes. FromBar and ToBar are
// the one-based song bars it shows (inclusive). Scaled is true when the time
// axis was averaged to fit the width cap: such an overview shows the form,
// and the pages show the detail.
type Picture struct {
	File    string `json:"file"`
	Kind    string `json:"kind"` // spectrogram, chroma, rhythm or energy
	FromBar int    `json:"from_bar,omitempty"`
	ToBar   int    `json:"to_bar,omitempty"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Scaled  bool   `json:"scaled"`
}

// Images returns spectrogram.png, chroma.png and rhythm.png, and the other
// pictures, at the default width cap. The bytes depend only on the analysis.
func (a *Analysis) Images() (map[string][]byte, error) {
	files, _, err := a.Pictures(DefaultImageWidth)
	return files, err
}

// Pictures renders every image audition -o writes and lists them in write
// order. spectrogram.png and chroma.png show the whole range, averaged in time
// when wider than maxWidth pixels; then the range is also split into pages of
// whole bars (spectrogram-bars-001-008.png and chroma-bars-001-008.png) at
// full resolution, each at most maxWidth wide. A maxWidth of zero or less
// keeps one full-resolution strip of each.
func (a *Analysis) Pictures(maxWidth int) (map[string][]byte, []Picture, error) {
	out := map[string][]byte{}
	var list []Picture
	add := func(name, kind string, img *image.NRGBA, b0, b1 int, scaled bool) error {
		data, err := encode(img)
		if err != nil {
			return err
		}
		out[name] = data
		p := Picture{File: name, Kind: kind, Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), Scaled: scaled}
		if b1 > b0 {
			p.FromBar, p.ToBar = a.firstBarIdx+b0+1, a.firstBarIdx+b1
		}
		list = append(list, p)
		return nil
	}
	bars := max(0, len(a.barCols)-1)
	if maxWidth <= 0 {
		maxWidth = math.MaxInt32
	}
	spec, scaled := a.spectrogram(0, bars, maxWidth)
	if err := add("spectrogram.png", "spectrogram", spec, 0, bars, scaled); err != nil {
		return nil, nil, err
	}
	chroma, chromaScaled := a.chromaImage(0, bars, maxWidth)
	if err := add("chroma.png", "chroma", chroma, 0, bars, chromaScaled); err != nil {
		return nil, nil, err
	}
	if err := add("rhythm.png", "rhythm", a.rhythmImage(), 0, 0, false); err != nil {
		return nil, nil, err
	}
	if scaled || chromaScaled {
		per := a.barsPerPage(bars, maxWidth)
		for b0 := 0; b0 < bars; b0 += per {
			b1 := min(bars, b0+per)
			suffix := fmt.Sprintf("bars-%03d-%03d.png", a.firstBarIdx+b0+1, a.firstBarIdx+b1)
			img, s := a.spectrogram(b0, b1, maxWidth)
			if err := add("spectrogram-"+suffix, "spectrogram", img, b0, b1, s); err != nil {
				return nil, nil, err
			}
			img, s = a.chromaImage(b0, b1, maxWidth)
			if err := add("chroma-"+suffix, "chroma", img, b0, b1, s); err != nil {
				return nil, nil, err
			}
		}
	}
	if a.mixLen > 0 && len(a.curve) > 0 {
		if err := add("energy.png", "energy", a.energyImage(maxWidth), 0, bars, false); err != nil {
			return nil, nil, err
		}
	}
	return out, list, nil
}

const chromaCell = 3

// barsPerPage is the largest of 32, 16, 8, 4, 2 and 1 bars whose pages all
// fit the width at full resolution (1 when even one bar does not).
func (a *Analysis) barsPerPage(bars, maxWidth int) int {
	for _, n := range []int{32, 16, 8, 4, 2} {
		fits := true
		for b0 := 0; b0 < bars && fits; b0 += n {
			b1 := min(bars, b0+n)
			fits = a.barCols[b1]-a.barCols[b0]+1 <= maxWidth && (b1-b0)*16*chromaCell <= maxWidth
		}
		if fits {
			return n
		}
	}
	return 1
}

// columnsFor maps output column x of width w to source columns [c0, c1) of
// a span of n columns starting at first.
func columnsFor(x, w, n, first int) (int, int) {
	c0 := first + x*n/w
	c1 := max(c0+1, first+(x+1)*n/w)
	return c0, c1
}

// spectrogram draws bars [b0, b1): log frequency 20 Hz to 20 kHz, high
// frequencies at the top, 70 dB of range below the loudest cell of the whole
// range. It is scaled (power averaged over columns) when wider than maxWidth.
func (a *Analysis) spectrogram(b0, b1, maxWidth int) (*image.NRGBA, bool) {
	first, last := 0, len(a.specRows)
	if b1 > b0 && b1 < len(a.barCols) {
		first, last = max(0, min(a.barCols[b0], len(a.specRows))), max(0, min(a.barCols[b1], len(a.specRows)))
		if b1 == len(a.barCols)-1 {
			last = len(a.specRows)
		}
	}
	n := max(1, last-first)
	width, scaled := n, false
	if width > maxWidth {
		width, scaled = maxWidth, true
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, specRows*2))
	peak := float32(-200)
	for _, row := range a.specRows {
		for _, v := range row {
			peak = max(peak, v)
		}
	}
	column := make([]float64, specRows)
	for x := 0; x < width; x++ {
		c0, c1 := columnsFor(x, width, n, first)
		for r := range column {
			column[r] = 0
		}
		count := 0
		for c := c0; c < c1 && c < len(a.specRows); c++ {
			for r, v := range a.specRows[c] {
				if scaled {
					column[r] += math.Pow(10, float64(v)/10)
				} else {
					column[r] = float64(v)
				}
			}
			count++
		}
		for r := range column {
			v := -200.0
			switch {
			case count == 0:
			case scaled:
				v = 10 * math.Log10(column[r]/float64(count)+1e-30)
			default:
				v = column[r]
			}
			c := ramp((v - float64(peak-70)) / 70)
			y := (specRows - 1 - r) * 2
			img.SetNRGBA(x, y, c)
			img.SetNRGBA(x, y+1, c)
		}
	}
	lastLabel := -1000
	for i, col := range a.barCols {
		x := col - first
		if scaled {
			x = (col - first) * width / n
		}
		if x < 0 || x >= width {
			continue
		}
		level := uint8(140)
		if a.barNumbers[i]%4 == 0 {
			level = 255
		}
		for y := 0; y < specRows*2; y++ {
			img.SetNRGBA(x, y, color.NRGBA{level, level, level, 255})
		}
		if i < len(a.barCols)-1 && (x-lastLabel >= 28 || lastLabel < 0) {
			drawText(img, x+2, 2, strconv.Itoa(a.barNumbers[i]+1), 2, color.NRGBA{255, 255, 255, 255})
			lastLabel = x
		}
	}
	ratio := math.Pow(specHighHz/specLowHz, 1.0/specRows)
	for _, tick := range []struct {
		hz    float64
		label string
	}{{30, "30"}, {50, "50"}, {100, "100"}, {200, "200"}, {500, "500"}, {1000, "1k"}, {2000, "2k"}, {5000, "5k"}, {10000, "10k"}} {
		row := int(math.Log(tick.hz/specLowHz) / math.Log(ratio))
		y := (specRows-1-row)*2 + 1
		for x := 0; x < min(width, 6); x++ {
			img.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
		}
		drawText(img, 8, y-5, tick.label, 2, color.NRGBA{255, 255, 255, 255})
	}
	return img, scaled
}

// chromaImage draws bars [b0, b1) of the pitched stems' chroma: B at the top,
// C at the bottom, a white line at each bar.
func (a *Analysis) chromaImage(b0, b1, maxWidth int) (*image.NRGBA, bool) {
	const ch = 14
	first, last := b0*16, min(len(a.chroma), b1*16)
	if b1 <= b0 {
		first, last = 0, len(a.chroma)
	}
	n := max(1, last-first)
	width, scaled, cell := n*chromaCell, false, chromaCell
	if width > maxWidth {
		width, scaled, cell = maxWidth, true, 0
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, 12*ch))
	var mean [12]float64
	for x := 0; x < width; x++ {
		var c0, c1 int
		if scaled {
			c0, c1 = columnsFor(x, width, n, first)
		} else {
			c0 = first + x/cell
			c1 = c0 + 1
		}
		mean = [12]float64{}
		count := 0
		for c := c0; c < c1 && c < len(a.chroma); c++ {
			var top float64
			for _, v := range a.chroma[c] {
				top = math.Max(top, v)
			}
			if top > 0 {
				for pc, v := range a.chroma[c] {
					mean[pc] += v / top
				}
			}
			count++
		}
		for pc := range mean {
			col := ramp(0)
			if count > 0 {
				col = ramp(mean[pc] / float64(count))
			}
			for dy := 0; dy < ch; dy++ {
				img.SetNRGBA(x, (11-pc)*ch+dy, col)
			}
		}
	}
	for c := first; c < last; c += 16 {
		x := (c - first) * chromaCell
		if scaled {
			x = (c - first) * width / n
		}
		for y := 0; y < 12*ch; y++ {
			img.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}
	return img, scaled
}

// rhythmImage has one row per sounding track, one cell per sixteenth.
func (a *Analysis) rhythmImage() *image.NRGBA {
	const cell = 24
	rows := max(1, len(a.rhythmRows))
	rhythm := image.NewNRGBA(image.Rect(0, 0, 16*cell, rows*cell))
	for r := 0; r < rows; r++ {
		for s := 0; s < 16; s++ {
			col := ramp(0)
			if r < len(a.rhythmRows) {
				col = ramp(a.rhythmRows[r][s])
			}
			for dy := 0; dy < cell; dy++ {
				for dx := 0; dx < cell; dx++ {
					rhythm.SetNRGBA(s*cell+dx, r*cell+dy, col)
				}
			}
		}
	}
	return rhythm
}

// energyImage plots the mix's short-term loudness (3 s window, every 100 ms,
// drawn where the window ends) over the range: grid lines every 6 LU labelled
// in LUFS at the right edge, a dim line per bar and a brighter one every
// fourth bar, a white line and bar number (at the bottom) at each section
// start, and each section's integrated loudness as an orange segment.
func (a *Analysis) energyImage(maxWidth int) *image.NRGBA {
	const height = 300
	width := max(400, len(a.curve))
	width = min(width, maxWidth)
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{12, 12, 16, 255})
		}
	}
	top := math.Inf(-1)
	for _, v := range a.curve {
		if !math.IsNaN(v) {
			top = math.Max(top, v)
		}
	}
	if math.IsInf(top, -1) {
		top = -6
	}
	hi := 6 * math.Ceil((top+1)/6)
	lo := hi - 36
	yOf := func(v float64) int {
		return int(math.Round((hi - math.Max(lo, math.Min(hi, v))) / (hi - lo) * (height - 1)))
	}
	xOf := func(sample int) int { return min(width-1, max(0, int(int64(sample)*int64(width)/int64(a.mixLen)))) }
	for i, s := range a.barSamples {
		c := uint8(36)
		if i%4 == 0 {
			c = 64
		}
		x := xOf(s)
		for y := 0; y < height; y++ {
			img.SetNRGBA(x, y, color.NRGBA{c, c, c + 8, 255})
		}
	}
	for v := hi; v >= lo; v -= 6 {
		y := yOf(v)
		for x := 0; x < width; x++ {
			if img.NRGBAAt(x, y).R < 40 {
				img.SetNRGBA(x, y, color.NRGBA{40, 40, 52, 255})
			}
		}
		label := strconv.Itoa(int(v))
		drawText(img, width-len(label)*8-4, max(0, y-11), label, 2, color.NRGBA{150, 150, 170, 255})
	}
	for _, m := range a.sectionMarks {
		x0, x1 := xOf(m.from), xOf(m.to)
		for y := 0; y < height; y++ {
			img.SetNRGBA(x0, y, color.NRGBA{230, 230, 230, 255})
		}
		if !math.IsNaN(m.integrated) {
			y := yOf(m.integrated)
			for x := x0; x <= x1 && x < width; x++ {
				img.SetNRGBA(x, y, color.NRGBA{245, 125, 21, 255})
				img.SetNRGBA(x, min(height-1, y+1), color.NRGBA{245, 125, 21, 255})
			}
		}
		drawText(img, x0+3, height-14, strconv.Itoa(m.fromBar), 2, color.NRGBA{255, 255, 255, 255})
	}
	px, py := -1, -1
	for i, v := range a.curve {
		if math.IsNaN(v) {
			px = -1
			continue
		}
		x, y := xOf((i+1)*a.curveHop), yOf(v)
		if px >= 0 {
			drawLine(img, px, py, x, y, color.NRGBA{255, 255, 255, 255})
		}
		px, py = x, y
	}
	return img
}

func drawLine(img *image.NRGBA, x0, y0, x1, y1 int, c color.NRGBA) {
	steps := max(abs(x1-x0), abs(y1-y0), 1)
	for i := 0; i <= steps; i++ {
		img.SetNRGBA(x0+(x1-x0)*i/steps, y0+(y1-y0)*i/steps, c)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// glyphs is a 3x5 pixel font for the labels: digits, '-' and 'k'.
var glyphs = map[rune][5]string{
	'0': {"111", "101", "101", "101", "111"}, '1': {"010", "110", "010", "010", "111"},
	'2': {"111", "001", "111", "100", "111"}, '3': {"111", "001", "111", "001", "111"},
	'4': {"101", "101", "111", "001", "001"}, '5': {"111", "100", "111", "001", "111"},
	'6': {"111", "100", "111", "101", "111"}, '7': {"111", "001", "010", "010", "010"},
	'8': {"111", "101", "111", "101", "111"}, '9': {"111", "101", "111", "001", "111"},
	'-': {"000", "000", "111", "000", "000"}, 'k': {"100", "101", "110", "101", "101"},
}

// drawText writes text at (x, y) with each font pixel scale pixels wide, on a
// black box so it reads over any colour.
func drawText(img *image.NRGBA, x, y int, text string, scale int, c color.NRGBA) {
	b := img.Bounds()
	w := len(text)*4*scale + scale
	for dy := -scale; dy < 6*scale; dy++ {
		for dx := -scale; dx < w; dx++ {
			if p := image.Pt(x+dx, y+dy); p.In(b) {
				img.SetNRGBA(p.X, p.Y, color.NRGBA{0, 0, 0, 255})
			}
		}
	}
	for i, r := range text {
		g, ok := glyphs[r]
		if !ok {
			continue
		}
		for gy, row := range g {
			for gx, bit := range row {
				if bit != '1' {
					continue
				}
				for sy := 0; sy < scale; sy++ {
					for sx := 0; sx < scale; sx++ {
						if p := image.Pt(x+i*4*scale+gx*scale+sx, y+gy*scale+sy); p.In(b) {
							img.SetNRGBA(p.X, p.Y, c)
						}
					}
				}
			}
		}
	}
}
