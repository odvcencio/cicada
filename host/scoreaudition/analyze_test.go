package scoreaudition

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

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
	if a.Report.Mismatches != len(a.Report.Bars) {
		t.Errorf("%d of %d bars flagged, want all", a.Report.Mismatches, len(a.Report.Bars))
	}
	for _, b := range a.Report.Bars {
		if b.Match != "disagree" || b.Written == "" {
			t.Errorf("bar %d: %+v", b.Bar, b)
		}
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
	if len(a.Report.Score.Swing) != 1 || a.Report.Score.Swing[0].Pattern != "swung" {
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
