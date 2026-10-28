package render_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/cmplx"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

// The score plays middle C on the first step of a bar. The 50 percent gate of a
// 1/16 step at 120 BPM ends after 62.5 ms, and the vibraphone damper then needs
// 70 ms to fall 60 dB.
const modalPedalHead = "cicada 2\ntempo 120\nseed 7\n"

const modalPedalPattern = "pattern line notes { gate = 50% c4 . . . . . . . . . . . . . . . }\n"

type modalPedalRender struct {
	left      []float32
	noteOffAt int // the frame where the gate ends
}

// renderModalPedal renders a score to floating-point WAV and reports where the
// note-off landed, from the render's own note tap.
func renderModalPedal(t *testing.T, source string, bars int) modalPedalRender {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil {
		t.Fatal(diagnostics)
	}
	result := modalPedalRender{noteOffAt: -1}
	opts := render.Options{SampleRate: 48000, Bits: 32, Block: 128, TailSec: 2, NoteTap: func(track int, event seq.Event) {
		if event.Kind == seq.NoteOff && result.noteOffAt < 0 {
			result.noteOffAt = int(event.Sample)
		}
	}}
	if bars > 0 {
		opts.Bars = bars
	}
	var wav bytes.Buffer
	report, err := render.WAV(score, opts, &wav)
	if err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()[44:]
	result.left = make([]float32, int(report.Frames))
	for i := range result.left {
		result.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*8:]))
	}
	return result
}

func modalWindowEnergy(x []float32, from, to int) float64 {
	var sum float64
	for _, v := range x[from:to] {
		sum += float64(v) * float64(v)
	}
	return sum / float64(to-from)
}

// modalToneMagnitude is the magnitude of x at one frequency over a Hann window, so
// a loud neighbour at another pitch leaks little into the reading.
func modalToneMagnitude(x []float32, from, to int, hz float64) float64 {
	var sum complex128
	var weight float64
	for n := from; n < to; n++ {
		w := .5 - .5*math.Cos(2*math.Pi*float64(n-from)/float64(to-from))
		sum += complex(float64(x[n])*w, 0) * cmplx.Exp(complex(0, -2*math.Pi*hz*float64(n)/48000))
		weight += w
	}
	return cmplx.Abs(sum) / weight
}

// Both render paths hold the same score: an arrange block plays through the
// engine's schedule, and a song through the direct mixer.
var modalPedalForms = []struct{ name, arrange string }{
	{"arrange", "arrange { place a vibes line { at = @1.1.1 length = 1bar } }\n"},
	{"song", "scene a { vibes = line }\nsong { a }\n"},
}

func TestVibraphonePedalKeepsAGatedNoteSoundingAfterTheNoteOff(t *testing.T) {
	for _, form := range modalPedalForms {
		t.Run(form.name, func(t *testing.T) {
			pedal := renderModalPedal(t, modalPedalHead+"track vibes model_vibraphone { sustain = on }\n"+modalPedalPattern+form.arrange, 0)
			plain := renderModalPedal(t, modalPedalHead+"track vibes model_vibraphone { }\n"+modalPedalPattern+form.arrange, 0)
			if pedal.noteOffAt <= 0 || pedal.noteOffAt != plain.noteOffAt {
				t.Fatalf("note-off at frame %d with the pedal, %d without", pedal.noteOffAt, plain.noteOffAt)
			}
			// 100 to 300 ms after the note-off, past the damper's 70 ms.
			from, to := pedal.noteOffAt+4800, pedal.noteOffAt+14400
			ringing, damped := modalWindowEnergy(pedal.left, from, to), modalWindowEnergy(plain.left, from, to)
			t.Logf("METRIC vibraphone %s note_off_frame=%d mean_square_100_to_300ms_after pedal=%.3e damped=%.3e", form.name, pedal.noteOffAt, ringing, damped)
			if ringing < 1e-4 {
				t.Fatalf("with the pedal down only %.3e was left after the note-off", ringing)
			}
			if damped > ringing*1e-6 {
				t.Fatalf("without the pedal %.3e was left after the note-off, against %.3e with it", damped, ringing)
			}
			// Until the note-off the pedal changes nothing.
			for n := 0; n < pedal.noteOffAt; n++ {
				if pedal.left[n] != plain.left[n] {
					t.Fatalf("frame %d differs before the note-off", n)
				}
			}
		})
	}
}

func TestVibraphonePedalLiftDampsTheNote(t *testing.T) {
	// Each case renders the score with the pedal left down and with a lift, and
	// compares what is left of the note after the lift.
	const down = "track vibes model_vibraphone { sustain = on }\n"
	const lane = "automate vibes.sustain {\n  @1.1.1 1\n  @1.3.1 0 step\n}\n"
	for _, tc := range []struct {
		name, kept, lifted string
		from, to           int // the frames to compare, after the lift
	}{
		// The lane lifts the pedal at one second, the third beat of the bar.
		{"arrange automation", down + modalPedalPattern + modalPedalForms[0].arrange, down + lane + modalPedalPattern + modalPedalForms[0].arrange, 52800, 72000},
		{"song automation", down + modalPedalPattern + modalPedalForms[1].arrange, down + lane + modalPedalPattern + modalPedalForms[1].arrange, 52800, 72000},
		// The second scene starts at two seconds and sets the pedal.
		{"song scene setting", down + modalPedalPattern + "scene a { vibes = line }\nscene b { vibes = off }\nsong { a b }\n",
			down + modalPedalPattern + "scene a { vibes = line }\nscene b { vibes = off vibes.sustain = 0 }\nsong { a b }\n", 2*48000 + 4800, 2*48000 + 24000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kept := modalWindowEnergy(renderModalPedal(t, modalPedalHead+tc.kept, 0).left, tc.from, tc.to)
			lifted := modalWindowEnergy(renderModalPedal(t, modalPedalHead+tc.lifted, 0).left, tc.from, tc.to)
			t.Logf("METRIC vibraphone %s mean_square_after_lift pedal_kept=%.3e pedal_lifted=%.3e", tc.name, kept, lifted)
			// The note decays with its own 4 s fall while the pedal is down, so
			// the floor is far above a damped bar's and well below its strike.
			if kept < 1e-7 {
				t.Fatalf("the note stopped sounding with the pedal down: %.3e", kept)
			}
			if lifted > kept*1e-6 {
				t.Fatalf("lifting the pedal left %.3e, against %.3e with it kept down", lifted, kept)
			}
		})
	}
}

func TestVibraphoneRepedalClearsTheOldBarAndKeepsTheNewOne(t *testing.T) {
	// C rings from the start and G is struck on beat 2, each held for a beat.
	// A pedal lifted on the G and pressed again a sixteenth later, while the G
	// is still held, clears the C and leaves the G ringing past its key-up.
	const c4, g4 = 261.6256, 391.9954
	pattern := "pattern line notes { gate = 100% c4 - - - g4 - - - . . . . . . . . }\n"
	arrange := "arrange { place a vibes line { at = @1.1.1 length = 1bar } }\n"
	held := renderModalPedal(t, modalPedalHead+"track vibes model_vibraphone { sustain = on }\n"+pattern+arrange, 0)
	repedal := renderModalPedal(t, modalPedalHead+"track vibes model_vibraphone { }\nautomate vibes.sustain {\n  @1.1.1 1\n  @1.2.1 0 step\n  @1.2.2 1 step\n}\n"+pattern+arrange, 0)
	// The G's key-up is at one second. Look at 1.1 to 1.6 seconds.
	from, to := 52800, 76800
	cHeld, gHeld := modalToneMagnitude(held.left, from, to, c4), modalToneMagnitude(held.left, from, to, g4)
	cRepedal, gRepedal := modalToneMagnitude(repedal.left, from, to, c4), modalToneMagnitude(repedal.left, from, to, g4)
	t.Logf("METRIC vibraphone repedal magnitude C held=%.3e repedaled=%.3e, G held=%.3e repedaled=%.3e", cHeld, cRepedal, gHeld, gRepedal)
	if cHeld < 1e-3 || gHeld < 1e-3 {
		t.Fatalf("a held pedal should leave both bars ringing: C %.3e, G %.3e", cHeld, gHeld)
	}
	if cRepedal > cHeld*1e-3 {
		t.Fatalf("the re-pedal left the old bar at %.3e, from %.3e", cRepedal, cHeld)
	}
	if gRepedal < gHeld*.5 {
		t.Fatalf("the re-pedal damped the new bar: %.3e, from %.3e", gRepedal, gHeld)
	}
	// A note whose key-up comes while the pedal is still up is damped, so a
	// gate shorter than the lift loses its own bar.
	short := renderModalPedal(t, modalPedalHead+"track vibes model_vibraphone { }\nautomate vibes.sustain {\n  @1.1.1 1\n  @1.2.1 0 step\n  @1.2.2 1 step\n}\npattern line notes { gate = 50% c4 . . . g4 . . . . . . . . . . . }\n"+arrange, 0)
	if g := modalToneMagnitude(short.left, from, to, g4); g > gHeld*1e-3 {
		t.Fatalf("a gate that ends while the pedal is up left the bar at %.3e, from %.3e", g, gHeld)
	}
}

// modalNativeOfflineDelta renders a song score offline and through the native
// engine, and returns the largest sample difference.
func modalNativeOfflineDelta(t *testing.T, source string) float64 {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil {
		t.Fatal(diagnostics)
	}
	opts := render.Options{SampleRate: 48000, Bits: 32, Block: 128}
	var wav bytes.Buffer
	report, err := render.WAV(score, opts, &wav)
	if err != nil {
		t.Fatal(err)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatal(diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	native.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	limiter, _ := mix.NewLimiter(48000)
	latency := limiter.LatencyFrames()
	var l, r [128]float32
	var peak float64
	sounded := false
	for offset := 0; offset < int(report.Frames)+latency; offset += 128 {
		native.Render(l[:], r[:])
		for j := 0; j < 128; j++ {
			frame := offset + j - latency
			if frame < 0 || frame >= int(report.Frames)-128 {
				continue
			}
			want := math.Float32frombits(binary.LittleEndian.Uint32(wav.Bytes()[44+frame*8:]))
			peak = max(peak, math.Abs(float64(l[j]-want)))
			sounded = sounded || l[j] != 0
		}
	}
	if !sounded {
		t.Fatal("the native engine was silent")
	}
	return peak
}

func TestVibraphonePedalNativeAndOfflineRendersAgree(t *testing.T) {
	pattern := "pattern line notes { gate = 50% c4 . . . g4 . . . e4 . . . . . . . }\n"
	// With gate = 100% every step boundary is a key-up, so a pedal change on a
	// step lands on the instant of a key-up. The offline mixer applies a change
	// before that instant's key-ups and the engine after them. The damper follows
	// the state of key and pedal, not their order, so the two must still agree.
	contiguous := "pattern line notes { gate = 100% c4 d4 e4 f4 g4 a4 b4 c5 c4 . . . . . . g4 }\n"
	for name, source := range map[string]string{
		"track setting":             modalPedalHead + "track vibes model_vibraphone { sustain = on }\n" + pattern + "scene a { vibes = line }\nsong { a a }\n",
		"scene setting":             modalPedalHead + "track vibes model_vibraphone { }\n" + pattern + "scene a { vibes = line vibes.sustain = 1 }\nscene b { vibes = line vibes.sustain = 0 }\nsong { a b }\n",
		"automation":                modalPedalHead + "track vibes model_vibraphone { }\n" + pattern + "automate vibes.sustain {\n  @1.1.1 1\n  @1.3.1 0 step\n  @1.3.2 1 step\n  @2.1.1 0 step\n}\nscene a { vibes = line }\nsong { a a }\n",
		"press on a key-up":         modalPedalHead + "track vibes model_vibraphone { }\n" + contiguous + "automate vibes.sustain {\n  @1.1.1 0\n  @1.1.3 1 step\n}\nscene a { vibes = line }\nsong { a }\n",
		"lift on a key-up":          modalPedalHead + "track vibes model_vibraphone { }\n" + contiguous + "automate vibes.sustain {\n  @1.1.1 1\n  @1.1.3 0 step\n  @1.2.1 1 step\n}\nscene a { vibes = line }\nsong { a }\n",
		"scene setting on a key-up": modalPedalHead + "track vibes model_vibraphone { }\n" + contiguous + "scene a { vibes = line }\nscene b { vibes = line vibes.sustain = 1 }\nsong { a b }\n",
	} {
		t.Run(name, func(t *testing.T) {
			delta := modalNativeOfflineDelta(t, source)
			t.Logf("METRIC vibraphone pedal %s native_offline_peak_delta=%g", name, delta)
			if delta > 1e-6 {
				t.Fatalf("native and offline renders differ by %g", delta)
			}
		})
	}
}
