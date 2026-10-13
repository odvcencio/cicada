package scoreaudition

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
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

// Images returns spectrogram.png, chroma.png and rhythm.png. The bytes depend
// only on the analysis.
func (a *Analysis) Images() (map[string][]byte, error) {
	out := map[string][]byte{}
	// Spectrogram: log frequency 40 Hz to 12 kHz, high frequencies at the top.
	frames := len(a.specRows)
	if frames == 0 {
		frames = 1
	}
	spec := image.NewNRGBA(image.Rect(0, 0, frames, specRows*2))
	peak := float32(-200)
	for _, row := range a.specRows {
		for _, v := range row {
			if v > peak {
				peak = v
			}
		}
	}
	for x, row := range a.specRows {
		for r, v := range row {
			c := ramp(float64((v - (peak - 70)) / 70))
			y := (specRows - 1 - r) * 2
			spec.SetNRGBA(x, y, c)
			spec.SetNRGBA(x, y+1, c)
		}
	}
	for i, col := range a.barCols {
		if col < 0 || col >= frames {
			continue
		}
		level := uint8(140)
		if a.barNumbers[i]%4 == 0 {
			level = 255
		}
		for y := 0; y < specRows*2; y++ {
			spec.SetNRGBA(col, y, color.NRGBA{level, level, level, 255})
		}
	}
	var err error
	if out["spectrogram.png"], err = encode(spec); err != nil {
		return nil, err
	}

	// Chroma: one column per sixteenth, B at the top and C at the bottom.
	const cw, ch = 3, 14
	cols := len(a.chroma)
	if cols == 0 {
		cols = 1
	}
	chroma := image.NewNRGBA(image.Rect(0, 0, cols*cw, 12*ch))
	for x, c := range a.chroma {
		var top float64
		for _, v := range c {
			top = math.Max(top, v)
		}
		for pc, v := range c {
			col := ramp(0)
			if top > 0 {
				col = ramp(v / top)
			}
			y0 := (11 - pc) * ch
			for dy := 0; dy < ch; dy++ {
				for dx := 0; dx < cw; dx++ {
					chroma.SetNRGBA(x*cw+dx, y0+dy, col)
				}
			}
		}
	}
	for x := 0; x < cols; x += 16 {
		for y := 0; y < 12*ch; y++ {
			chroma.SetNRGBA(x*cw, y, color.NRGBA{255, 255, 255, 255})
		}
	}
	if out["chroma.png"], err = encode(chroma); err != nil {
		return nil, err
	}

	// Rhythm: one row per sounding track, one cell per sixteenth.
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
	if out["rhythm.png"], err = encode(rhythm); err != nil {
		return nil, err
	}
	return out, nil
}
