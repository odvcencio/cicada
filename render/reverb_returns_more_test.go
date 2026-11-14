package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// A short room beside a long tank, the first thing producers asked for.
const tankAndRoom = "fx tank reverb { size = 1.4 decay = 4800ms damp = 3500Hz highpass = 250Hz }\n" +
	"fx room reverb { size = 0.6 decay = 700ms damp = 6000Hz highpass = 180Hz predelay = 8ms }\n"

const returnsBody = `
instrument pad { voice poly { out = saw(pitch) * adsr(gate, 5ms, 100ms, 0.8, 300ms) * velocity * 0.2 } }
track keys pad { level = -6dB SENDS }
pattern chords { step = 1/4 [c4 e4 g4] - . . | [a3 c4 e4] - . . }
scene main { keys = chords }
song { main }
`

func returnsScore(t *testing.T, effects, sends string) *notation.Score {
	t.Helper()
	source := "cicada 2\ntempo 120\n" + effects + strings.Replace(returnsBody, "SENDS", sends, 1)
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	return score
}

// renderFloats renders a score through WAV, which picks the render path, as
// 32-bit float samples (interleaved stereo).
func renderFloats(t *testing.T, score *notation.Score, tail float64) []float32 {
	t.Helper()
	var out bytes.Buffer
	if _, err := WAV(score, Options{Bits: 32, TailSec: tail}, &out); err != nil {
		t.Fatal(err)
	}
	return wavFloats(t, out.Bytes())
}

func wavFloats(t *testing.T, wav []byte) []float32 {
	t.Helper()
	at := bytes.Index(wav, []byte("data"))
	if at < 0 || at+8 > len(wav) {
		t.Fatal("no data chunk")
	}
	pcm := wav[at+8:]
	if size := int(binary.LittleEndian.Uint32(wav[at+4:])); size <= len(pcm) {
		pcm = pcm[:size]
	}
	samples := make([]float32, len(pcm)/4)
	for i := range samples {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*4:]))
	}
	return samples
}

// energy sums the squares of a - b (and of a - b - c + d when c and d are
// given) over the samples from frame start on.
func returnEnergy(a, b, c, d []float32, start int) float64 {
	sum := 0.0
	for i := start * 2; i < len(a); i++ {
		v := float64(a[i]) - float64(b[i])
		if c != nil {
			v = v - float64(c[i]) + float64(d[i])
		}
		sum += v * v
	}
	return sum
}

func decibels(ratio float64) float64 { return 10 * math.Log10(ratio) }

// A second reverb beside a tank: each return is audible on its own, each
// keeps its own decay, and with both sent to, the mix is the dry signal plus
// the tank's contribution plus the room's, as each makes it alone. Every
// version renders through the engine, because each declares both reverbs.
func TestSecondReverbBesideTankIsAudibleAndIndependent(t *testing.T) {
	const tail = 3
	dry := renderFloats(t, returnsScore(t, tankAndRoom, ""), tail)
	tank := renderFloats(t, returnsScore(t, tankAndRoom, "send tank = -6dB"), tail)
	room := renderFloats(t, returnsScore(t, tankAndRoom, "send room = -6dB"), tail)
	both := renderFloats(t, returnsScore(t, tankAndRoom, "send tank = -6dB send room = -6dB"), tail)
	if len(dry) != len(tank) || len(dry) != len(room) || len(dry) != len(both) {
		t.Fatalf("lengths %d %d %d %d", len(dry), len(tank), len(room), len(both))
	}
	signal := returnEnergy(dry, make([]float32, len(dry)), nil, nil, 0)
	tankPart, roomPart := returnEnergy(tank, dry, nil, nil, 0), returnEnergy(room, dry, nil, nil, 0)
	if signal == 0 || tankPart < signal*1e-3 || roomPart < signal*1e-3 {
		t.Fatalf("returns are not audible: dry %g, tank %.1f dB, room %.1f dB against it", signal, decibels(tankPart/signal), decibels(roomPart/signal))
	}
	// The mix adds each return once, so both sends together leave only
	// float rounding against the sum of the two single-send mixes.
	residual := returnEnergy(both, tank, room, dry, 0)
	if residual > (tankPart+roomPart)*1e-10 {
		t.Fatalf("the two returns interact: residual %.1f dB under their sum", decibels((tankPart+roomPart)/residual))
	}
	// Each return keeps its own settings: two seconds into the tail the
	// 4.8-second tank still rings and the 0.7-second room has died away.
	late := (len(dry)/2 - 48000) // the last second
	tankLate, roomLate := returnEnergy(tank, dry, nil, nil, late), returnEnergy(room, dry, nil, nil, late)
	if tankLate < roomLate*100 {
		t.Fatalf("in the last second the tank is only %.1f dB over the room", decibels(tankLate/roomLate))
	}
	t.Logf("tank %.1f dB and room %.1f dB against the dry signal; residual %.1f dB under them; last second tank over room %.1f dB",
		decibels(tankPart/signal), decibels(roomPart/signal), decibels((tankPart+roomPart)/max(residual, 1e-300)), decibels(tankLate/roomLate))
}

// The returns join the mix in slot order, the echo (slot A), then the first
// reverb (B), then later reverbs (C to H), wherever the echo is declared.
func TestReturnSumOrderFollowsSlots(t *testing.T) {
	const echo = "fx echo delay { time = 3/16 feedback = 0.4 }\n"
	const tank = "fx tank reverb { size = 1.4 decay = 4800ms }\n"
	const room = "fx room reverb { size = 0.6 decay = 700ms }\n"
	const sends = "send echo = -12dB send tank = -9dB send room = -12dB"
	want := scheduledBytes(t, returnsScore(t, echo+tank+room, sends))
	for _, order := range []string{tank + echo + room, tank + room + echo} {
		if !bytes.Equal(scheduledBytes(t, returnsScore(t, order, sends)), want) {
			t.Fatalf("declaring the echo elsewhere changed the mix:\n%s", order)
		}
	}
	// The same reverbs with the room declared first swap slots B and C, so
	// they are added in the other order; that is a different score.
	if bytes.Equal(scheduledBytes(t, returnsScore(t, echo+room+tank, sends)), want) {
		t.Log("swapping the reverbs' slots happened to give the same bytes")
	}
}

// Both render paths agree: a score with one reverb renders on the direct
// mixer, and the same score with a silent second reverb renders on the
// engine, with the sounding reverb in slot C. They write the same bytes.
func TestRenderPathsAgreeWithAnExtraReverb(t *testing.T) {
	const room = "fx room reverb { size = 0.6 decay = 900ms damp = 6000Hz }\n"
	alone := returnsScore(t, room, "send room = -12dB")
	second := returnsScore(t, "fx idle reverb { size = 1.5 decay = 8s }\n"+room, "send room = -12dB")
	if UsesScheduledEngine(alone, Options{}) || !UsesScheduledEngine(second, Options{}) {
		t.Fatal("one reverb keeps the direct mixer; two take the engine")
	}
	var direct, engine bytes.Buffer
	if _, err := WAV(alone, Options{Bits: 32, TailSec: 1}, &direct); err != nil {
		t.Fatal(err)
	}
	if _, err := WAV(second, Options{Bits: 32, TailSec: 1}, &engine); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(direct.Bytes(), engine.Bytes()) {
		t.Fatal("the direct mixer's slot B and the engine's slot C rendered different bytes")
	}
}

// A reverb declared through a preset is a reverb return like any other, so a
// score whose second reverb comes from a preset takes the engine. (The preset's
// target, hall, stays a return only because a track sends to it; otherwise it
// is a prototype and preset resolution drops it.)
func TestPresetReverbCountsAsAReturn(t *testing.T) {
	const effects = "fx hall reverb { size = 1.4 decay = 4s }\npreset small { instrument = hall size = 0.5 decay = 600ms }\nfx room small {}\n"
	score := returnsScore(t, effects, "send hall = -9dB send room = -6dB")
	if !UsesScheduledEngine(score, Options{}) {
		t.Fatal("a preset-declared second reverb kept the direct mixer, which has one reverb return")
	}
	hallOnly := renderFloats(t, returnsScore(t, effects, "send hall = -9dB"), 1)
	both := renderFloats(t, score, 1)
	if returnEnergy(both, hallOnly, nil, nil, 0) == 0 {
		t.Fatal("the send to the preset reverb was not heard")
	}
}
