package schedule_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// tailEnergy renders one c4 on a pack sampler that carries the extra
// declaration lines, and returns the sum of squares over 0.20 s to 0.28 s. At
// 120 bpm the note ends at 0.125 s, so the window holds only the release.
func tailEnergy(t *testing.T, extra string) float64 {
	t.Helper()
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{ID: "env", Frames: 48000})
	sampler := strings.Replace(pack.Sampler("s", 2), "  voices = 2\n", "  voices = 2\n"+extra, 1)
	source := "cicada 2\ntempo 120\n" + sampler + "track t s {}\npattern hit notes steps=4 { c4 . . . }\nscene main { t = hit }\nsong { main }\n"
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("%+v\n%s", ds, source)
	}
	var wav bytes.Buffer
	if _, err := render.WAV(score, render.Options{AssetRoot: dir, SampleRate: 48000, Bits: 32, Block: 256, Bars: 1}, &wav); err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	at := bytes.Index(data, []byte("data"))
	if at < 0 {
		t.Fatal("no data chunk")
	}
	pcm := data[at+8:]
	var sum, peak float64
	for frame := 0; (frame+1)*8 <= len(pcm); frame++ {
		l := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8:])))
		peak = math.Max(peak, math.Abs(l))
		if frame >= 9600 && frame < 13440 {
			sum += l * l
		}
	}
	if peak == 0 {
		t.Fatal("silent render")
	}
	return sum
}

func TestPackSamplerReleaseOverrideShortensTheTail(t *testing.T) {
	manifest := tailEnergy(t, "")
	short := tailEnergy(t, "  release = 30ms\n")
	same := tailEnergy(t, "  release = 180ms\n")
	if manifest <= 0 || short >= manifest/10 {
		t.Fatalf("tail energy: manifest %g, release=30ms %g; want under a tenth", manifest, short)
	}
	if same != manifest {
		t.Fatalf("release=180ms matches the manifest value but rendered %g, not %g", same, manifest)
	}
}
