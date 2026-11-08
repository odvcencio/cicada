package render_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// renderKit renders one bar of a score at 120 bpm (sixteenth = 6000 frames) and
// returns the left and right float32 channels.
func renderKit(t *testing.T, dir, source string) (left, right []float64) {
	t.Helper()
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
	for frame := 0; (frame+1)*8 <= len(pcm); frame++ {
		left = append(left, float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8:]))))
		right = append(right, float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8+4:]))))
	}
	return left, right
}

func peak(x []float64, from, to int) float64 {
	var p float64
	for i := from; i < min(to, len(x)); i++ {
		p = math.Max(p, math.Abs(x[i]))
	}
	return p
}

func energy(x []float64, from, to int) float64 {
	var e float64
	for i := from; i < min(to, len(x)); i++ {
		e += x[i] * x[i]
	}
	return e
}

func kitScore(pack testpack.Pack, rows string) string {
	return "cicada 2\ntempo 120\n" + pack.Sampler("shells", 8) +
		"kit rec {\n  sd = shells.d2\n  ch = shells.f#2\n  oh = shells.a#2\n  bd = builtin.bd\n}\n" +
		"track drums rec { level = -6dB }\npattern p drums steps=16 {\n" + rows + "}\nscene main { drums = p }\nsong { main }\n"
}

func kitPack(t *testing.T, dir string) testpack.Pack { return kitPackChoke(t, dir, 1) }

// kitPackChoke writes d2 = 38, f#2 = 42 and a#2 = 46. The two hats share the
// given choke group; zero means none.
func kitPackChoke(t *testing.T, dir string, group int) testpack.Pack {
	return testpack.Write(t, dir, testpack.Options{ID: fmt.Sprintf("kit%d", group), Frames: 48000, Channels: 2, Zones: []testpack.Zone{
		{KeyLow: 38, KeyHigh: 38, OneShot: true},
		{KeyLow: 42, KeyHigh: 42, ChokeGroup: group, OneShot: true},
		{KeyLow: 46, KeyHigh: 46, ChokeGroup: group, OneShot: true},
	}})
}

func TestKitSamplerLaneHitLevelFollowsVelocity(t *testing.T) {
	dir := t.TempDir()
	pack := kitPack(t, dir)
	soft, softR := renderKit(t, dir, kitScore(pack, "  sd: x2... .... .... ....\n"))
	loud, _ := renderKit(t, dir, kitScore(pack, "  sd: x9... .... .... ....\n"))
	ps, pl := peak(soft, 0, 6000), peak(loud, 0, 6000)
	if ps == 0 || pl < 3*ps {
		t.Fatalf("peak at x2 %g and at x9 %g; x9 should be at least 3x louder", ps, pl)
	}
	if peak(softR, 0, 6000) == 0 {
		t.Fatal("right channel is silent: a stereo pack must stay stereo")
	}
}

func TestKitSamplerLanesShareChokeGroup(t *testing.T) {
	dir := t.TempDir()
	// Both hats play the same 1 s recording. The closed hat on step 2 rings
	// from 0.25 s, so a window from 0.4 s holds its tail, plus the open hat's
	// tail unless the pack's choke group cut it.
	window := func(x []float64) float64 { return energy(x, 19200, 33600) }
	rows := "  oh: x... .... .... ....\n  ch: ..x. .... .... ....\n"
	closed, _ := renderKit(t, dir, kitScore(kitPack(t, dir), "  ch: ..x. .... .... ....\n"))
	choked, _ := renderKit(t, dir, kitScore(kitPack(t, dir), rows))
	free, _ := renderKit(t, dir, kitScore(kitPackChoke(t, dir, 0), rows))
	if window(closed) <= 0 || math.Abs(window(choked)-window(closed)) > .02*window(closed) {
		t.Fatalf("choked energy %g should match the closed hat alone %g", window(choked), window(closed))
	}
	if window(free) < 1.5*window(closed) {
		t.Fatalf("without a choke group the open hat's tail should add: %g against %g", window(free), window(closed))
	}
}

func TestKitMixesSamplerAndBuiltinLanes(t *testing.T) {
	dir := t.TempDir()
	pack := kitPack(t, dir)
	l, _ := renderKit(t, dir, kitScore(pack, "  bd: x... .... .... ....\n  sd: .... x... .... ....\n"))
	if peak(l, 0, 6000) == 0 || peak(l, 24000, 30000) == 0 {
		t.Fatal("a lane of the mixed kit is silent")
	}
}

func TestKitSamplerLaneLevelControl(t *testing.T) {
	dir := t.TempDir()
	pack := kitPack(t, dir)
	base, _ := renderKit(t, dir, kitScore(pack, "  sd: x9... .... .... ....\n"))
	quiet, _ := renderKit(t, dir, strings.Replace(kitScore(pack, "  sd: x9... .... .... ....\n"), "level = -6dB", "level = -6dB sd_level = -12dB", 1))
	pb, pq := peak(base, 0, 6000), peak(quiet, 0, 6000)
	if pb == 0 || math.Abs(pq/pb-math.Pow(10, -12.0/20)) > .02 {
		t.Fatalf("sd_level -12 dB changed the peak by %g, want %g", pq/pb, math.Pow(10, -12.0/20))
	}
}

func TestHumanizeShiftsHitVelocityAndDefaultIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	pack := kitPack(t, dir)
	rows := "  sd: x5... x5... x5... x5...\n"
	plain, _ := renderKit(t, dir, kitScore(pack, rows))
	zero, _ := renderKit(t, dir, strings.Replace(kitScore(pack, rows), "steps=16", "steps=16 humanize=0", 1))
	if !equalFloats(plain, zero) {
		t.Fatal("humanize = 0 changed the output")
	}
	human, _ := renderKit(t, dir, strings.Replace(kitScore(pack, rows), "steps=16", "steps=16 humanize=12", 1))
	again, _ := renderKit(t, dir, strings.Replace(kitScore(pack, rows), "steps=16", "steps=16 humanize=12", 1))
	if !equalFloats(human, again) {
		t.Fatal("humanize is not deterministic")
	}
	differs := false
	for hit := 0; hit < 4; hit++ {
		if peak(plain, hit*24000, hit*24000+6000) != peak(human, hit*24000, hit*24000+6000) {
			differs = true
		}
	}
	if !differs {
		t.Fatal("humanize = 12 left every hit at its written velocity")
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
