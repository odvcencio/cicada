package padsampler

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/audition"
)

func testAudio(n int) audition.Audio {
	a := audition.Audio{Rate: Rate, Left: make([]float32, n), Right: make([]float32, n)}
	for i := range a.Left {
		a.Left[i] = float32(.25 * math.Sin(float64(i)*.071))
		a.Right[i] = float32(.15 * math.Cos(float64(i)*.113))
	}
	return a
}

func TestStereoChopsPitchReverseAndSourcePreservation(t *testing.T) {
	a := testAudio(48001)
	data, err := WAV(a)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil || !reflect.DeepEqual(a, decoded) {
		t.Fatalf("stereo round trip: %v", err)
	}
	s := NewStore(t.TempDir(), "main.cicada")
	src, err := s.Import(data, strings.Repeat("海", 40))
	if err != nil {
		t.Fatal(err)
	}
	b := DefaultBank()
	b.Sources = []Source{src}
	if err := Chop(&b, src.ID, 16); err != nil {
		t.Fatal(err)
	}
	for i, p := range b.Pads {
		if err := p.Validate(src.Asset.Frames); err != nil {
			t.Fatalf("chop %d: %v", i, err)
		}
		if i > 0 && p.Start != b.Pads[i-1].End {
			t.Fatal("chops leave a gap")
		}
	}
	if b.Pads[15].End != src.Asset.Frames {
		t.Fatal("last chop loses the remainder")
	}
	p := b.Pads[0]
	p.Start = 0
	p.End = 24000
	p.GainDB = 0
	p.Pitch = 12
	p.Reverse = true
	prepared, err := Prepare(a, p, Clean())
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Left) != 12000 || prepared.Rate != Rate {
		t.Fatalf("octave should halve duration: %d", len(prepared.Left))
	}
	p.Pitch = 0
	reversed, err := Prepare(a, p, Clean())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(reversed.Left[0]-a.Left[23999])) > .00001 {
		t.Fatal("reverse starts at the wrong source frame")
	}
	if !reflect.DeepEqual(a, decoded) {
		t.Fatal("preparation mutated original PCM")
	}
	original, _ := os.ReadFile(filepath.Join(s.Root, src.Asset.Path))
	if !bytes.Equal(original, data) {
		t.Fatal("chopping changed the source file")
	}
}

func TestEffectsAreFiniteAudibleAndBypassStable(t *testing.T) {
	a := testAudio(4800)
	p := Pad{Name: "tone", End: 4800, Mode: "oneshot", ReleaseMS: 5}
	clean, err := Prepare(a, p, Clean())
	if err != nil {
		t.Fatal(err)
	}
	e := Clean()
	e.Cutoff = 2300
	e.Drive = 9
	e.DelayMix = .3
	e.ReverbMix = .18
	wet, err := Prepare(a, p, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(wet.Left) <= len(clean.Left) || reflect.DeepEqual(wet.Left[:4800], clean.Left) {
		t.Fatal("effects did not change the sound or add a tail")
	}
	for i, x := range wet.Left {
		if !finite(float64(x)) || math.Abs(float64(x)) > .892 || !finite(float64(wet.Right[i])) {
			t.Fatal("invalid effect output")
		}
	}
	p.BypassEffects = true
	bypass, err := Prepare(a, p, e)
	if err != nil || !reflect.DeepEqual(clean, bypass) {
		t.Fatalf("resampled pad applied bank effects again: %v", err)
	}
	src := Source{ID: strings.Repeat("a", 64)}
	if SoundID(p, e, src) != SoundID(p, Clean(), src) {
		t.Fatal("bypass changes with bank effects")
	}
}

func TestPrintUsesImmutableSoundsAndReleasesGatesAndChokes(t *testing.T) {
	a := audition.Audio{Rate: Rate, Left: make([]float32, 4000), Right: make([]float32, 4000)}
	for i := range a.Left {
		a.Left[i] = .2
		a.Right[i] = -.1
	}
	voices := map[string]Voice{"old": {Audio: a, Pad: Pad{Mode: "gate", Choke: 1}}, "new": {Audio: a, Pad: Pad{Mode: "oneshot", Choke: 1}}}
	perf := Performance{Frames: 4000, Hits: []Hit{{Sound: "old", Pad: 1, Velocity: 1, GateFrames: 480, Choke: 1}, {Sound: "new", Pad: 2, Frame: 1000, Velocity: .5, Choke: 1}}}
	out, err := Print(perf, voices)
	if err != nil {
		t.Fatal(err)
	}
	if out.Left[479] != .2 || out.Left[720] != 0 || out.Right[1100] != -.05 || out.Left[3999] >= .001 {
		t.Fatalf("gate, stereo or final fade mismatch: %v %v %v %v", out.Left[479], out.Left[720], out.Right[1100], out.Left[3999])
	}
	perf.Hits[0].GateFrames = 0
	out, err = Print(perf, voices)
	if err != nil || out.Left[1240] != .1 {
		t.Fatalf("choke did not stop first voice: %v", err)
	}
	second, _ := Print(perf, voices)
	if !reflect.DeepEqual(out, second) {
		t.Fatal("print is nondeterministic")
	}
	for _, bad := range []Hit{{Sound: "missing", Pad: 1, Velocity: 1}, {Sound: "old", Pad: 1, Velocity: math.NaN(), Choke: 1}, {Sound: "old", Pad: 1, Velocity: 1, Frame: 4000, Choke: 1}, {Sound: "old", Pad: 1, Velocity: 1, Choke: 4}} {
		if _, err = Print(Performance{Frames: 4000, Hits: []Hit{bad}}, voices); err == nil {
			t.Fatalf("bad hit accepted: %+v", bad)
		}
	}
}

func TestStoreRejectsTamperedPreparedPCMAndEscapingPaths(t *testing.T) {
	s := NewStore(t.TempDir(), "main.cicada")
	data, _ := WAV(testAudio(4800))
	src, err := s.Import(data, "tone")
	if err != nil {
		t.Fatal(err)
	}
	p := Pad{Name: "tone", Source: src.ID, End: 4800, Mode: "oneshot", ReleaseMS: 5}
	sound, err := s.Prepare(p, Clean(), src)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Root, sound.Asset.Path), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Audio(sound.Asset); err == nil {
		t.Fatal("tampered PCM was accepted")
	}
	if _, err = s.Sound("../outside"); err == nil {
		t.Fatal("path accepted as a sound identifier")
	}
	if err = s.Write("../outside", data, false); err == nil {
		t.Fatal("write escaped project")
	}
}
