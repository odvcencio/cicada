package scoreaudition

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestDefaultMeterOutputCompatibility(t *testing.T) {
	a := analyze(t, skankMajor)
	data, err := json.Marshal(a.Report)
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.Images()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"report":          "fb9525c468e0736094f485639f174913dd762ad57edfe1d477a762af7487a6db",
		"text":            "2254624ac1eca5ddcc93c2fa43f32211a890cd09b911ea178ccf7eccb9e091d8",
		"spectrogram.png": "dbd38d46ef659b4dce5dc9ce2e8dc9eddf12d9ecaaa7db7b613ce52d0abcf417",
		"chroma.png":      "4d73221459189aa065fa40f81c28aeb877d4729ca67f01835af85f5e4dba5371",
		"rhythm.png":      "8aba28738374c037ffce5ca2a6dadaaf833c6da64fd679e9b4e448c1f6674472",
		"energy.png":      "50a987b2a8232a11f9be416471a38f5b010b6cc7a94bdb6c959f7d9195ac2e24",
	}
	files["report"], files["text"] = data, []byte(textOf(a))
	for name, hash := range want {
		if got := fmt.Sprintf("%x", sha256.Sum256(files[name])); got != hash {
			t.Errorf("%s hash %s, want %s", name, got, hash)
		}
	}
}

func analyze(t *testing.T, source string) *Analysis {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatalf("parse: %s", d.Error())
		}
	}
	a, err := Analyze(score, Options{TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func track(t *testing.T, a *Analysis, id string) TrackReport {
	t.Helper()
	for _, tr := range a.Report.Tracks {
		if tr.ID == id {
			return tr
		}
	}
	t.Fatalf("no track %s", id)
	return TrackReport{}
}

func TestSkankMajor(t *testing.T) {
	a := analyze(t, skankMajor)
	t.Logf("%s", textOf(a))
	if got := a.Report.HeardKey[0].Key; got != "C major" {
		t.Errorf("heard key %s, want C major", got)
	}
	for _, id := range []string{"guitar", "piano"} {
		tr := track(t, a, id)
		best := 0
		for s, v := range tr.Rhythm {
			if v > tr.Rhythm[best] {
				best = s
			}
		}
		if best%4 != 2 {
			t.Errorf("%s rhythm %v peaks at slot %d, want an offbeat (3, 7, 11, 15)", id, tr.Rhythm, best+1)
		}
		var off, on float64
		for s, v := range tr.Rhythm {
			if s%4 == 2 {
				off += v
			} else {
				on += v
			}
		}
		if off < 3*on {
			t.Errorf("%s offbeat weight %.2f vs other %.2f", id, off, on)
		}
	}
	for _, b := range a.Report.Bars {
		if b.Masked {
			t.Errorf("bar %d masked at %.1f dB", b.Bar, b.AudibilityDB)
		}
		if b.Match != "agree" {
			t.Errorf("bar %d written %s heard %s: %s", b.Bar, b.Written, b.Heard, b.Match)
		}
	}
}

func TestHornsMinor(t *testing.T) {
	a := analyze(t, hornsMinor)
	t.Logf("%s", textOf(a))
	if got := a.Report.HeardKey[0].Key; !strings.HasSuffix(got, "minor") {
		t.Errorf("heard key %s, want a minor key", got)
	}
	if a.Report.MaskedBars != 0 {
		t.Errorf("%d horn bars masked", a.Report.MaskedBars)
	}
	horns, skank := track(t, a, "horns"), track(t, a, "skank")
	if horns.EnergySharePct < 60 || horns.LevelVsLoudestDB != 0 {
		t.Errorf("horns share %.1f%%, vs loudest %.1f dB", horns.EnergySharePct, horns.LevelVsLoudestDB)
	}
	if horns.Decay20Ms < 500 || !horns.Decay20LowerBound {
		t.Errorf("horn decay20 %.0f ms (lower bound %v), want long", horns.Decay20Ms, horns.Decay20LowerBound)
	}
	if skank.Decay20Ms >= horns.Decay20Ms {
		t.Errorf("skank decay20 %.0f should be under horn %.0f", skank.Decay20Ms, horns.Decay20Ms)
	}
}

func TestDecay20ShortAndLong(t *testing.T) {
	a := analyze(t, noteLengths)
	t.Logf("%s", textOf(a))
	stab, drone := track(t, a, "stab"), track(t, a, "drone")
	if stab.Decay20Ms > 120 {
		t.Errorf("stab decay20 %.0f ms, want short", stab.Decay20Ms)
	}
	if drone.Decay20Ms < 1000 || !drone.Decay20LowerBound {
		t.Errorf("drone decay20 %.0f ms (lower bound %v), want long", drone.Decay20Ms, drone.Decay20LowerBound)
	}
}

func TestMaskedPartFlagged(t *testing.T) {
	a := analyze(t, maskedPart)
	t.Logf("%s", textOf(a))
	if a.Report.MaskedBars != len(a.Report.Bars) {
		t.Errorf("%d of %d bars masked, want all", a.Report.MaskedBars, len(a.Report.Bars))
	}
	for _, b := range a.Report.Bars {
		if !b.Masked || b.Written == "" || b.AudibilityDB > maskedBelowDB {
			t.Errorf("bar %d: %+v", b.Bar, b)
		}
	}
}

// A drum-heavy mix whose pitched part plays the written chords: the harmony
// is right but hard to hear, so the bars read agree and MASKED, never DISAGREE.
func TestDrumHeavyCorrectHarmonyIsMaskedNotDisagree(t *testing.T) {
	a := analyze(t, drumHeavy)
	t.Logf("%s", textOf(a))
	if a.Report.MaskedBars != len(a.Report.Bars) {
		t.Errorf("%d of %d bars masked, want all", a.Report.MaskedBars, len(a.Report.Bars))
	}
	for _, b := range a.Report.Bars {
		if b.Match != "agree" || !b.Masked {
			t.Errorf("bar %d written %s heard %s match %s masked %v (%.1f dB)", b.Bar, b.Written, b.Heard, b.Match, b.Masked, b.AudibilityDB)
		}
	}
	if got := a.Report.HeardKey[0].Key; got != "F major" {
		t.Errorf("heard key %s, want F major (from the pitched stems)", got)
	}
	if a.Report.AudibilityDB > maskedBelowDB {
		t.Errorf("overall audibility %.1f dB", a.Report.AudibilityDB)
	}
}

func TestFirstAcidHeardFromPitchedStems(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	a := analyze(t, string(source))
	t.Logf("%s", textOf(a))
	found := false
	for _, k := range a.Report.HeardKey {
		found = found || k.Key == "A minor"
	}
	if !found {
		t.Errorf("A minor is not in the heard key %+v", a.Report.HeardKey)
	}
	if a.Report.Mismatches != 0 {
		t.Errorf("%d bars disagree, want 0 (the written harmony is played)", a.Report.Mismatches)
	}
	if len(a.Report.FullMixKey) != 3 || a.Report.FullMixKey[0].Key == "A minor" {
		t.Errorf("full mix key %+v: expected drums to pull it away from A minor", a.Report.FullMixKey)
	}
	if bass := track(t, a, "bass"); !bass.Masked || bass.LevelVsLoudestDB > -20 {
		t.Errorf("bass %+v should be masked and far under the drums", bass)
	}
}

func TestSwingGroupedByValue(t *testing.T) {
	a := analyze(t, swungTwoPatterns)
	g := a.Report.Score.Swing
	if len(g) != 1 || g[0].SwingPercent != 60 || strings.Join(g[0].Tracks, ",") != "high,low" || len(g[0].Patterns) != 2 {
		t.Errorf("swing groups %+v", g)
	}
	if text := textOf(a); !strings.Contains(text, "swing 60%: high, low") {
		t.Errorf("text lacks grouped swing line:\n%s", text)
	}
}

func TestShuffleSwungSlots(t *testing.T) {
	a := analyze(t, shuffle)
	t.Logf("%s", textOf(a))
	tr := track(t, a, "ticks")
	if tr.SwingPercent != 66 {
		t.Errorf("swing %v, want 66", tr.SwingPercent)
	}
	for s := 0; s < 16; s++ {
		if s%2 == 1 && tr.Rhythm[s] < 0.6 {
			t.Errorf("swung slot %d strength %.2f: %v", s+1, tr.Rhythm[s], tr.Rhythm)
		}
		if s%2 == 0 && tr.Rhythm[s] > 0.4 {
			t.Errorf("straight slot %d strength %.2f: %v", s+1, tr.Rhythm[s], tr.Rhythm)
		}
	}
	if len(a.Report.Score.Swing) != 1 || a.Report.Score.Swing[0].Patterns[0] != "swung" {
		t.Errorf("swing facts %+v", a.Report.Score.Swing)
	}
}

func TestRangeSelection(t *testing.T) {
	score, _ := notation.Parse([]byte(skankMajor))
	a, err := Analyze(score, Options{From: 1, Bars: 2, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Report.Bars) != 2 || a.Report.Bars[0].Bar != 2 || a.Report.Bars[0].Written != "F" {
		t.Errorf("bars %+v", a.Report.Bars)
	}
}

func TestDeterministic(t *testing.T) {
	var jsons, pngs [2][]byte
	for i := range jsons {
		a := analyze(t, skankMajor)
		j, err := json.Marshal(a.Report)
		if err != nil {
			t.Fatal(err)
		}
		jsons[i] = j
		imgs, err := a.Images()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"spectrogram.png", "chroma.png", "rhythm.png"} {
			pngs[i] = append(pngs[i], imgs[name]...)
			if !bytes.HasPrefix(imgs[name], []byte("\x89PNG")) {
				t.Errorf("%s is not a PNG", name)
			}
		}
	}
	if !bytes.Equal(jsons[0], jsons[1]) {
		t.Error("JSON differs between runs")
	}
	if !bytes.Equal(pngs[0], pngs[1]) {
		t.Error("PNG bytes differ between runs")
	}
}

func textOf(a *Analysis) string {
	var b bytes.Buffer
	a.Report.WriteText(&b)
	return b.String()
}

func TestMeteredAuditionBarAndDefaultJSON(t *testing.T) {
	source := `cicada 2
meter 3/4
tempo 120
track bass acid {}
pattern riff { . . . . . . . . . . . . c3 . . . . . . . . . . . }
scene a { bass = riff }
song { a*8 }
`
	a := analyze(t, source)
	tr := track(t, a, "bass")
	if len(tr.Rhythm) != 12 || tr.Rhythm[0] == 0 {
		t.Fatalf("3/4 rhythm should place the bar 2 onset at slot 1: %v", tr.Rhythm)
	}
	if len(a.Report.Bars) < 2 || a.Report.Bars[1].Bar != 2 || len(a.Report.Bars[1].WrittenNotes) == 0 {
		t.Fatalf("bar 2: %+v", a.Report.Bars)
	}
	if !strings.Contains(textOf(a), "step 1-12 (swing)") {
		t.Fatal("text rhythm heading does not follow meter")
	}
	img, scaled := a.chromaImage(0, 1, 10000)
	if scaled || img.Bounds().Dx() != 12*chromaCell {
		t.Fatalf("3/4 chroma dimensions: %v scaled=%v", img.Bounds(), scaled)
	}
	img, _ = a.chromaImage(0, 2, 10000)
	if got := img.NRGBAAt(12*chromaCell, 0); got.R != 255 || got.G != 255 || got.B != 255 {
		t.Fatalf("missing bar line at 3/4 boundary: %v", got)
	}
	if len(a.Report.Loudness.ShortTermPerBeatLUFS) != 24 {
		t.Fatalf("3/4 beat measurements: %d", len(a.Report.Loudness.ShortTermPerBeatLUFS))
	}
	files, pictures, err := a.Pictures(400)
	if err != nil {
		t.Fatal(err)
	}
	var pages []string
	for _, p := range pictures {
		if p.Kind == "rhythm" && p.Width != 12*24 {
			t.Errorf("rhythm width %d", p.Width)
		}
		if strings.HasPrefix(p.File, "chroma-bars-") {
			pages = append(pages, fmt.Sprintf("%d-%d:%d", p.FromBar, p.ToBar, p.Width))
		}
	}
	if got := strings.Join(pages, " "); got != "1-4:144 5-8:144" {
		t.Errorf("pages %s", got)
	}
	energy, err := png.Decode(bytes.NewReader(files["energy.png"]))
	if err != nil {
		t.Fatal(err)
	}
	for b := 1; b < 8; b++ {
		x := b * 3 * a.rate / 2 * energy.Bounds().Dx() / a.mixLen
		want := color.RGBA{36, 36, 44, 255}
		if b%4 == 0 {
			want = color.RGBA{64, 64, 72, 255}
		}
		if got := color.RGBAModel.Convert(energy.At(x, 17)).(color.RGBA); got != want {
			t.Errorf("energy bar %d at %d: %v want %v", b+1, x, got, want)
		}
	}
	plain := analyze(t, strings.Replace(source, "meter 3/4\n", "", 1))
	data, err := json.Marshal(plain.Report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"ticks_per_bar"`)) || bytes.Contains(data, []byte(`"meter"`)) {
		t.Fatalf("default meter fields: %s", data)
	}
}

func TestSevenEightAuditionGrouping(t *testing.T) {
	a := analyze(t, `cicada 2
meter 7/8 2+2+3
tempo 120
track bass acid {}
pattern riff { c3 . . . . . . . . . . . . . }
scene a { bass = riff }
song { a*1 }
`)
	tr := track(t, a, "bass")
	if len(tr.Rhythm) != 14 || len(a.Report.Loudness.ShortTermPerBeatLUFS) != 7 {
		t.Fatalf("rhythm %d beats %d", len(tr.Rhythm), len(a.Report.Loudness.ShortTermPerBeatLUFS))
	}
	row := rhythmRow(tr.Rhythm, a.Report.rhythmGroups)
	if parts := strings.Fields(row); len(parts) != 3 || len([]rune(parts[0])) != 4 || len([]rune(parts[1])) != 4 || len([]rune(parts[2])) != 6 || !strings.Contains(textOf(a), row) {
		t.Errorf("rhythm row %q", row)
	}
	b := analyze(t, `cicada 2
meter 6/8
tempo 120
track bass acid {}
pattern riff { c3 . . . . . . . . . . . }
scene a { bass = riff }
song { a*1 }
`)
	parts := strings.Fields(rhythmRow(track(t, b, "bass").Rhythm, b.Report.rhythmGroups))
	if len(parts) != 6 {
		t.Fatalf("6/8 beat groups %v", parts)
	}
	for _, part := range parts {
		if len([]rune(part)) != 2 {
			t.Errorf("6/8 beat group %q", part)
		}
	}
}

func TestShortBarHeldNoteSounds(t *testing.T) {
	a := analyze(t, `cicada 2
meter 2/4
tempo 240
instrument tone { voice mono { out = sine(pitch) * adsr(gate, 5ms, 10ms, 1, 10ms) * velocity * 0.05 } }
track bed tone {}
pattern held notes { gate = 100% a3 - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - }
scene first { bed = held }
song { first*4 }
`)
	tr := track(t, a, "bed")
	if tr.ActiveBars != 4 {
		t.Errorf("active bars %d", tr.ActiveBars)
	}
	if len(tr.Sounding) < 4 || !tr.Sounding[1] || !tr.Sounding[2] || !tr.Sounding[3] || len(tr.SoundingSpans) != 1 || tr.SoundingSpans[0] != (BarSpan{1, 4}) {
		t.Errorf("sounding %v spans %v", tr.Sounding, tr.SoundingSpans)
	}
	for _, b := range a.Report.Bars[1:4] {
		if b.Sounding == nil || len(*b.Sounding) != 1 || (*b.Sounding)[0] != "bed" {
			t.Errorf("bar %d sounding %v", b.Bar, b.Sounding)
		}
	}
	if !strings.Contains(textOf(a), "████") {
		t.Error("activity row does not show four sounding bars")
	}
}

// A bar longer than the grace keeps the whole grace, whatever the meter (the
// shortest 4/4 bar, 0.8 s at 300 BPM, is one). A bar no longer than the grace
// tests its second half, so the window is never empty.
func TestGraceEndDependsOnTheBarLengthAlone(t *testing.T) {
	for _, c := range []struct{ lo, hi, grace, want int }{
		{0, 800, 500, 500},   // longer than the grace: unchanged
		{0, 501, 500, 500},   // just longer
		{0, 500, 500, 250},   // as long as the grace: its middle
		{100, 400, 500, 250}, // shorter: its middle
	} {
		if got := graceEnd(c.lo, c.hi, c.grace); got != c.want {
			t.Errorf("graceEnd(%d, %d, %d) = %d, want %d", c.lo, c.hi, c.grace, got, c.want)
		}
	}
}
