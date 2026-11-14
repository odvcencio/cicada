package render

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const reverbReturnsBody = `
instrument pad { voice poly { out = saw(pitch) * adsr(gate, 5ms, 100ms, 0.8, 300ms) * velocity * 0.2 } }
track keys pad { level = -6dB SENDS }
track bass pad { level = -9dB send room = -6dB pre }
pattern chords { step = 1/4 [c4 e4 g4] - . . | [a3 c4 e4] - . . }
pattern low { step = 1/4 c2 . g2 . }
scene main { keys = chords bass = low }
song { main*2 }
`

func reverbReturnsScore(t *testing.T, effects, sends string) *notation.Score {
	t.Helper()
	source := "cicada 2\ntempo 120\n" + effects + strings.Replace(reverbReturnsBody, "SENDS", sends, 1)
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	return score
}

func scheduledBytes(t *testing.T, score *notation.Score) []byte {
	t.Helper()
	var out bytes.Buffer
	if _, err := renderScheduleWAV(score, Options{Bits: 32, TailSec: 1}, &out, "", 1); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// A reverb in an extra slot (C to H) computes exactly what it computes in
// slot B: its sends use the same expressions and it joins the mix right after
// slot B. A first reverb that nothing sends to outputs exact zeros, so a
// score whose second reverb takes slot C renders the same bytes as the score
// with that reverb alone in slot B.
func TestExtraReverbReturnMatchesSlotB(t *testing.T) {
	const room = "fx room reverb { size = 0.6 decay = 900ms damp = 6000Hz }\n"
	alone := reverbReturnsScore(t, room, "send room = -12dB")
	second := reverbReturnsScore(t, "fx idle reverb { size = 1.5 decay = 8s }\n"+room, "send room = -12dB")
	if UsesScheduledEngine(alone, Options{}) || !UsesScheduledEngine(second, Options{}) {
		t.Fatal("one reverb keeps the direct mixer; two reverbs take the engine")
	}
	p, ds := project.FromScore(second)
	if p == nil {
		t.Fatal(ds)
	}
	if !bytes.Equal(scheduledBytes(t, alone), scheduledBytes(t, second)) {
		t.Fatal("the reverb in slot C rendered differently from the same reverb in slot B")
	}
	// Both reverbs sounding is a different mix from either alone.
	both := reverbReturnsScore(t, "fx hall reverb { size = 1.5 decay = 8s }\n"+room, "send room = -12dB send hall = -9dB")
	if bytes.Equal(scheduledBytes(t, both), scheduledBytes(t, alone)) {
		t.Fatal("a send to the second reverb changed nothing")
	}
}

// A score with one delay and one reverb compiles to the legacy slots and no
// routing, so its engine runs exactly the operations it ran before.
func TestOneReverbCompilesWithoutRouting(t *testing.T) {
	score := reverbReturnsScore(t, "fx echo delay {}\nfx room reverb {}\n", "send room = -12dB send echo = -20dB")
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 512)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routing != nil || cfg.ReverbB == nil || cfg.DelayA == nil {
		t.Fatalf("routing %+v, reverb %v, delay %v", cfg.Routing, cfg.ReverbB != nil, cfg.DelayA != nil)
	}
}

// stems refuses a score with extra reverb returns, whose stem folder could not
// sum to the mix yet; audition, which reads only track and master stems, may
// still render it.
func TestStemsRefuseExtraReverbReturns(t *testing.T) {
	score := reverbReturnsScore(t, "fx hall reverb {}\nfx room reverb {}\n", "send room = -12dB send hall = -9dB")
	_, err := Stems(score, Options{Bits: 32}, filepath.Join(t.TempDir(), "stems"))
	if err == nil || !strings.Contains(err.Error(), "room") {
		t.Fatalf("stems: %v, want a refusal naming room", err)
	}
	if _, err := Stems(score, Options{Bits: 32, TrackStemsOnly: true}, filepath.Join(t.TempDir(), "stems")); err != nil {
		t.Fatal(err)
	}
}
