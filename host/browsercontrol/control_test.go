package browsercontrol

import (
	"errors"
	"math"
	"testing"

	"m31labs.dev/cicada/host/demopolicy"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const silentScore = `tempo 120
key a minor
track bass acid { cutoff = 620Hz }
track drums drums {}
instrument tone {
 voice mono {
  let osc = saw(pitch)
  out = osc * env(gate, 50ms)
 }
}
track lead tone {}
pattern rest { . . . . }
pattern drumrest drums { bd: .... }
pattern leadrest notes { . . . . }
scene quiet {
 bass = rest
 drums = drumrest
 lead = leadrest
}
song {
 quiet*1
}
`

func fixture(t *testing.T) (*project.Project, engine.Config) {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(silentScore))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, diagnostics := project.FromScore(score)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	if err := project.ValidateProject(p); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	return p, cfg
}

func recordingController(t *testing.T) (*Controller, *[]cmd.Command) {
	t.Helper()
	p, cfg := fixture(t)
	var records []cmd.Command
	c, err := New(p, cfg, demopolicy.PublicDemo(), func(data []byte) error {
		for at := 0; at < len(data); at += cmd.CommandSize {
			record, err := cmd.DecodeCommand(data[at:at+cmd.CommandSize], uint8(cfg.Tracks))
			if err != nil {
				return err
			}
			records = append(records, record)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Play(); err != nil {
		t.Fatal(err)
	}
	return c, &records
}

func TestHeldNotesReleasePriorityAndRepeatAfterPanic(t *testing.T) {
	c, records := recordingController(t)
	if err := c.Down("keyA", "bass", 45, 100, false); err != nil {
		t.Fatal(err)
	}
	if err := c.Down("keyS", "bass", 48, 110, false); err != nil {
		t.Fatal(err)
	}
	before := len(*records)
	if err := c.Down("keyS", "bass", 48, 110, true); err != nil {
		t.Fatal(err)
	}
	if len(*records) != before {
		t.Fatal("repeat retriggered held note")
	}
	if err := c.Up("keyS"); err != nil {
		t.Fatal(err)
	}
	last := (*records)[len(*records)-1]
	if last.Op != cmd.OpNoteOn || last.Arg0&255 != 45 {
		t.Fatal("last-held release did not restore earlier note")
	}
	if err := c.Panic(); err != nil {
		t.Fatal(err)
	}
	before = len(*records)
	if err := c.Play(); !errors.Is(err, ErrResetRequired) || len(*records) != before {
		t.Fatal("stop allowed play before a fresh kernel was ready")
	}
	c.KernelResetReady() // Test sender models the host's fresh-kernel receipt.
	if err := c.Play(); err != nil {
		t.Fatal(err)
	}
	before = len(*records)
	if err := c.Down("keyA", "bass", 45, 100, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Down("keyA", "bass", 45, 100, false); err != nil {
		t.Fatal(err)
	}
	if len(*records) != before {
		t.Fatal("still-held input reopened after panic")
	}
	if err := c.Up("keyA"); err != nil {
		t.Fatal(err)
	}
	if err := c.Down("keyA", "bass", 45, 100, false); err != nil {
		t.Fatal(err)
	}
	if len(*records) != before+1 {
		t.Fatal("fresh press after release did not play")
	}
	if err := c.Up("keyA"); err != nil {
		t.Fatal(err)
	}
	if (*records)[len(*records)-1].Op != cmd.OpNoteOff {
		t.Fatal("final release did not release voice")
	}
}

func TestOutOfOrderReleaseAndDrumAliases(t *testing.T) {
	c, records := recordingController(t)
	if err := c.Down("older", "lead", 45, 100, false); err != nil {
		t.Fatal(err)
	}
	if err := c.Down("newer", "lead", 48, 100, false); err != nil {
		t.Fatal(err)
	}
	before := len(*records)
	if err := c.Up("older"); err != nil {
		t.Fatal(err)
	}
	if len(*records) != before {
		t.Fatal("old release stopped a newer mono note")
	}
	if err := c.Up("newer"); err != nil {
		t.Fatal(err)
	}
	if err := c.Down("midi41", "drums", 41, 100, false); err != nil {
		t.Fatal(err)
	}
	if err := c.Down("midi43", "drums", 43, 100, false); err != nil {
		t.Fatal(err)
	}
	before = len(*records)
	if err := c.Up("midi41"); err != nil {
		t.Fatal(err)
	}
	if len(*records) != before {
		t.Fatal("one drum alias stopped another held input")
	}
	if err := c.Up("midi43"); err != nil {
		t.Fatal(err)
	}
	if last := (*records)[len(*records)-1]; last.Op != cmd.OpNoteOff || last.Index != 6 {
		t.Fatal("last drum input released wrong lane")
	}
	for _, pair := range [][2]int{{36, 0}, {38, 1}, {42, 2}, {46, 3}, {39, 4}, {37, 5}, {41, 6}, {43, 6}, {45, 7}, {47, 7}, {48, 8}, {50, 8}, {56, 9}, {49, 10}, {57, 10}} {
		lane, ok := gmLane(pair[0])
		if !ok || int(lane) != pair[1] {
			t.Fatalf("GM %d -> %d, %v", pair[0], lane, ok)
		}
	}
}

func TestParameterValidationAndDemoCommandsNeverCallSender(t *testing.T) {
	c, records := recordingController(t)
	for _, value := range []float64{math.NaN(), math.Inf(1), -100, 1e100} {
		before := len(*records)
		if err := c.SetParam("bass.cutoff", &value); err == nil {
			t.Fatal("bad cutoff accepted")
		}
		if len(*records) != before {
			t.Fatal("rejected cutoff sent to DSP")
		}
	}
	cutoff := 1200.0
	if err := c.SetParam("bass.cutoff", &cutoff); err != nil {
		t.Fatal(err)
	}
	last := (*records)[len(*records)-1]
	if last.Op != cmd.OpSetParam || math.Float32frombits(last.Arg0) != 1200 {
		t.Fatal("wrong parameter ABI")
	}
	if err := c.SetMute("bass", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSolo("bass", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMute("missing", true); err == nil {
		t.Fatal("missing track accepted")
	}
	if err := c.Down("bad", "drums", 1, 100, false); err == nil {
		t.Fatal("unsupported MIDI drum accepted")
	}
	if err := c.Panic(); err != nil {
		t.Fatal(err)
	}
	if err := c.Down("new", "bass", 45, 100, false); !errors.Is(err, ErrNotPlaying) {
		t.Fatal("note reopened stopped transport")
	}
	p, cfg := fixture(t)
	called := false
	unconfigured, err := New(p, cfg, demopolicy.Policy{}, func([]byte) error { called = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = unconfigured.Play(); !errors.Is(err, demopolicy.ErrDenied) || called {
		t.Fatal("unset capability reached sender")
	}
}

func TestControlBytesDriveActualDSPAndPanicReleases(t *testing.T) {
	for _, track := range []string{"bass", "drums", "lead"} {
		t.Run(track, func(t *testing.T) {
			p, cfg := fixture(t)
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			c, err := New(p, cfg, demopolicy.PublicDemo(), func(data []byte) error {
				for at := 0; at < len(data); at += cmd.CommandSize {
					record, err := cmd.DecodeCommand(data[at:at+cmd.CommandSize], uint8(cfg.Tracks))
					if err != nil {
						return err
					}
					if !e.Push(record) {
						return errors.New("DSP queue rejected record")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Play(); err != nil {
				t.Fatal(err)
			}
			note := 45
			if track == "drums" {
				note = 36
			}
			if err = c.Down("live", track, note, 110, false); err != nil {
				t.Fatal(err)
			}
			var left, right [128]float32
			energy := 0.0
			for block := 0; block < 200; block++ {
				e.Render(left[:], right[:])
				for i := range left {
					if math.IsNaN(float64(left[i])) || math.IsInf(float64(left[i]), 0) {
						t.Fatal("nonfinite PCM")
					}
					energy += float64(left[i]*left[i] + right[i]*right[i])
				}
				drain(e)
			}
			if energy < 1e-7 {
				t.Fatal("live control path rendered silence")
			}
			if err = c.Panic(); err != nil {
				t.Fatal(err)
			}
			// Native DSP tails settle; the browser worklet additionally stops
			// rendering on OpStop, so its output is immediately zero.
			for block := 0; block < 2000; block++ {
				e.Render(left[:], right[:])
				drain(e)
			}
			for i := range left {
				if math.Abs(float64(left[i])) > 1e-6 || math.Abs(float64(right[i])) > 1e-6 {
					t.Fatal("panic retained sustained note")
				}
			}
			t.Logf("%s finite live PCM energy %.6f; panic released voice", track, energy)
		})
	}
}

func drain(e *engine.Engine) {
	var message cmd.Message
	for e.Poll(&message) {
	}
}
