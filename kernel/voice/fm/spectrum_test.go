package fm

import (
	"math"
	"math/cmplx"
	"testing"
)

// Measure the complete native voice at the same relative analysis windows used
// for the recorded-reference comparison. Bounds protect musical spectral range;
// they do not claim the reference's unknown MIDI velocity is matched.
func TestBellAndBassSpectralRangeAcrossVelocities(t *testing.T) {
	for _, test := range []struct {
		name                 string
		notes                []uint8
		start, end           float64
		middleMin, middleMax float64
		hardMin, ratioMin    float64
	}{
		{"bell_keys", []uint8{48}, .05, .35, 300, 450, 450, 1.7},
		{"bell_keys", []uint8{84, 88, 91}, .05, .35, 2700, 4500, 4000, 1.6},
		{"fm_bass", []uint8{48}, .015, .155, 130, 250, 500, 4},
	} {
		var centroids [3]float64
		for index, velocity := range []uint8{32, 80, 127} {
			f := mustInstrument(t, 48000, test.name)
			for _, note := range test.notes {
				if err := f.NoteOn(note, velocity); err != nil {
					t.Fatal(err)
				}
			}
			centroids[index] = voiceCentroid(f, test.start, test.end)
		}
		if centroids[1] < test.middleMin || centroids[1] > test.middleMax || centroids[2] < test.hardMin || centroids[2] < centroids[0]*test.ratioMin {
			t.Fatalf("patch=%s notes=%v centroids(v32,v80,v127)=%v outside spectral range", test.name, test.notes, centroids)
		}
		t.Logf("patch=%s notes=%v centroid_v32_hz=%.2f centroid_v80_hz=%.2f centroid_v127_hz=%.2f", test.name, test.notes, centroids[0], centroids[1], centroids[2])
	}
}

func voiceCentroid(f *Instrument, start, end float64) float64 {
	for range int(math.Round(start * 48000)) {
		f.NextStereo()
	}
	frames := int(math.Round((end - start) * 48000))
	size := 1
	for size < frames {
		size <<= 1
	}
	data := make([]complex128, size)
	var mean float64
	for i := range frames {
		l, r := f.NextStereo()
		value := float64(l+r) * .5
		data[i] = complex(value, 0)
		mean += value
	}
	mean /= float64(frames)
	for i := range frames {
		window := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(frames-1))
		data[i] = complex((real(data[i])-mean)*window, 0)
	}
	spectrumFFT(data)
	var power, weighted float64
	for i := 1; i <= size/2; i++ {
		hz := float64(i) * 48000 / float64(size)
		if hz < 40 || hz > 12000 {
			continue
		}
		value := real(data[i])*real(data[i]) + imag(data[i])*imag(data[i])
		power += value
		weighted += value * hz
	}
	return weighted / power
}

func spectrumFFT(data []complex128) {
	n := len(data)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}
	for span := 2; span <= n; span <<= 1 {
		root := cmplx.Rect(1, -2*math.Pi/float64(span))
		for base := 0; base < n; base += span {
			rotation := complex(1, 0)
			for i := 0; i < span/2; i++ {
				even, odd := data[base+i], data[base+i+span/2]*rotation
				data[base+i], data[base+i+span/2] = even+odd, even-odd
				rotation *= root
			}
		}
	}
}
