package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func sceneTempoScore(t *testing.T) *notation.Score {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "examples", "scene-tempo.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	return score
}

// ceilFrames is the sample at which a run of ticks ends at one tempo, rounded up
// the way the kernel clock rounds.
func ceilFrames(ticks, bpmMilli int64, rate int64) int64 {
	num := ticks * 60_000 * rate
	den := bpmMilli * seq.PPQ
	return (num + den - 1) / den
}

func TestSceneTempoWAVFramesAreTheSumOfTheSegments(t *testing.T) {
	score := sceneTempoScore(t)
	var wav bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000, TailSec: 1}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	// main*2 at 120, slower at 100, slowest at 84, outro*2 at 66.
	var want int64
	for _, segment := range []struct{ bars, bpm int64 }{{2, 120_000}, {1, 100_000}, {1, 84_000}, {2, 66_000}} {
		want += ceilFrames(segment.bars*seq.TicksPerBar, segment.bpm, 48_000)
	}
	want += 48_000
	if report.Frames != want || report.Bars != 6 {
		t.Fatalf("frames = %d (bars %d), want %d", report.Frames, report.Bars, want)
	}
	data := wav.Bytes()
	if got := int64(binary.LittleEndian.Uint32(data[40:44])) / 6; got != want {
		t.Fatalf("data chunk holds %d frames, want %d", got, want)
	}
	// Version 2 chunk: score tempo, bars, tail, then three (bar, tempo) steps.
	chunk := data[44+want*6:]
	if string(chunk[:4]) != "cica" || binary.LittleEndian.Uint32(chunk[4:8]) != 16+3*8 || binary.LittleEndian.Uint32(chunk[8:12]) != 2 {
		t.Fatalf("timing chunk header: % x", chunk[:12])
	}
	for i, step := range [][2]uint32{{2, 100_000}, {3, 84_000}, {4, 66_000}} {
		if binary.LittleEndian.Uint32(chunk[24+8*i:]) != step[0] || binary.LittleEndian.Uint32(chunk[28+8*i:]) != step[1] {
			t.Fatalf("tempo step %d: % x", i, chunk[24+8*i:32+8*i])
		}
	}
	if int64(binary.LittleEndian.Uint32(data[4:8])) != int64(len(data))-8 {
		t.Fatal("RIFF size does not cover the version 2 chunk")
	}
	path := filepath.Join(t.TempDir(), "scene-tempo.wav")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 6, TailSec: 1, PeakMaxDB: 0, DCMaxDB: 0})
	if err != nil || verified.Frames != want {
		t.Fatalf("verify-wav: %+v, %v", verified, err)
	}
	// Dropping a tempo step makes the length wrong, so verification fails.
	broken := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(broken[44+want*6+28:], 120_000)
	if err := os.WriteFile(path, broken, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 6, TailSec: 1, PeakMaxDB: 0, DCMaxDB: 0}); err == nil {
		t.Fatal("verify-wav accepted a tempo step that does not match the frame count")
	}
}

func TestVersion1TimingChunkStillVerifies(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(testScore))
	if len(diagnostics) != 0 {
		t.Fatalf("score diagnostics: %+v", diagnostics)
	}
	var wav bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000, Bars: 1, TailSec: .25}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	chunk := data[44+report.Frames*6:]
	if len(chunk) != 24 || binary.LittleEndian.Uint32(chunk[4:8]) != 16 || binary.LittleEndian.Uint32(chunk[8:12]) != 1 {
		t.Fatalf("a song with one tempo must keep the version 1 chunk: % x", chunk)
	}
	path := filepath.Join(t.TempDir(), "old.wav")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 1, TailSec: .25, PeakMaxDB: 0, DCMaxDB: 0}); err != nil {
		t.Fatal(err)
	}
}

func TestSceneTempoStemsVerify(t *testing.T) {
	score := sceneTempoScore(t)
	dir := filepath.Join(t.TempDir(), "stems")
	noDither := false
	report, err := Stems(score, Options{SampleRate: 48_000, TailSec: .5, Dither: &noDither}, dir)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80})
	if err != nil || verified.Frames != report.Frames {
		t.Fatalf("verify-stems: %+v, %v", verified, err)
	}
}

func TestSceneTempoBlockSizeDoesNotChangeTheRender(t *testing.T) {
	score := sceneTempoScore(t)
	noDither := false
	var first, second bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48_000, TailSec: .5, Block: 64, Dither: &noDither}, &first); err != nil {
		t.Fatal(err)
	}
	if _, err := WAV(score, Options{SampleRate: 48_000, TailSec: .5, Block: 128, Dither: &noDither}, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("offline render depends on block size across tempo changes")
	}
}

// envelope sums the energy of each window.
func envelope(a driftAudio, window int) []float64 {
	out := make([]float64, len(a.left)/window)
	for i := range out {
		for _, v := range a.left[i*window : (i+1)*window] {
			out[i] += float64(v) * float64(v)
		}
	}
	return out
}

func correlation(a, b []float64) float64 {
	var sa, sb, saa, sbb, sab float64
	n := float64(len(a))
	for i := range a {
		sa, sb, saa, sbb, sab = sa+a[i], sb+b[i], saa+a[i]*a[i], sbb+b[i]*b[i], sab+a[i]*b[i]
	}
	return (sab - sa*sb/n) / math.Sqrt((saa-sa*sa/n)*(sbb-sb*sb/n))
}

// The offline renderer and the kernel place notes on the same samples across
// the tempo steps: their energy envelopes follow each other.
func TestSceneTempoOfflineMatchesKernelTiming(t *testing.T) {
	score := sceneTempoScore(t)
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatal(diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	noDither := false
	var wav bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48_000, Bits: 32, TailSec: 0, Dither: &noDither}, &wav); err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	n := int(binary.LittleEndian.Uint32(data[40:44])) / 8
	offline := driftAudio{make([]float32, n), make([]float32, n)}
	for i := range offline.left {
		offline.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[44+i*8:]))
	}
	tempo, err := project.NewTempoMap(48_000, int64(p.TempoMilli), project.SongTempoChanges(p))
	if err != nil {
		t.Fatal(err)
	}
	if int64(n) != tempo.SampleAtTick(6*seq.TicksPerBar) {
		t.Fatalf("offline length %d, tempo map %d", n, tempo.SampleAtTick(6*seq.TicksPerBar))
	}
	player, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	native := driftAudio{make([]float32, n), make([]float32, n)}
	for at := 0; at < n; {
		frames := min(128, n-at)
		player.Render(native.left[at:at+frames], native.right[at:at+frames])
		var message cmd.Message
		for player.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine fault %d", message.A)
			}
		}
		at += frames
	}
	// Window of 20 ms; the limiter's latency shifts the offline file by a few ms.
	if c := correlation(envelope(offline, 960), envelope(native, 960)); c < .9 {
		t.Fatalf("offline and kernel energy envelopes correlate at %.3f", c)
	}
}

// One note starts on the first step and is held for 21 steps, so it ends five
// steps into the slower second bar; 27 rests fill the 48 steps of three bars.
var heldAcross = "tempo 120\ntrack bass acid {}\npattern held acid steps=48 { 1" + strings.Repeat(" -", 20) + strings.Repeat(" .", 27) + " }\nscene a { bass = held }\nscene b { bass = keep%s }\nsong { a b*2 }\n"

func renderBoth(t *testing.T, source string, rate int, bars int) (driftAudio, driftAudio) {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, rate, 128)
	if err != nil {
		t.Fatal(err)
	}
	noDither := false
	var wav bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: rate, Bits: 32, Bars: bars, TailSec: 1, Dither: &noDither}, &wav); err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	n := int(binary.LittleEndian.Uint32(data[40:44])) / 8
	offline := driftAudio{make([]float32, n), make([]float32, n)}
	for i := range offline.left {
		offline.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[44+i*8:]))
		offline.right[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[48+i*8:]))
	}
	tempo, err := project.NewTempoMap(rate, int64(p.TempoMilli), project.SongTempoChanges(p))
	if err != nil {
		t.Fatal(err)
	}
	end := int(tempo.SampleAtTick(int64(bars) * seq.TicksPerBar))
	latency := driftLatency(t, cfg)
	total := n + latency
	player, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	native := driftAudio{make([]float32, total), make([]float32, total)}
	for at := 0; at < total; {
		if at == end {
			player.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
		}
		frames := min(128, total-at)
		if at < end {
			frames = min(frames, end-at)
		}
		player.Render(native.left[at:at+frames], native.right[at:at+frames])
		var message cmd.Message
		for player.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine fault %d", message.A)
			}
		}
		at += frames
	}
	return offline, driftAudio{native.left[latency:], native.right[latency:]}
}

// A note held across a tempo step must end on the same sample in the offline
// render and in the kernel, at both common sample rates.
func TestSceneTempoHeldNoteMatchesKernelSampleForSample(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		offline, native := renderBoth(t, strings.Replace(heldAcross, "%s", " tempo = 60", 1), rate, 3)
		peak, _ := driftDelta(t, offline, native)
		var signal float64
		for _, v := range offline.left {
			signal = math.Max(signal, math.Abs(float64(v)))
		}
		if signal < .005 || peak != 0 {
			t.Fatalf("rate %d: signal peak %.4f, offline minus kernel peak %.9f", rate, signal, peak)
		}
		// The step moves the release: the same score without it sounds different.
		flat, _ := renderBoth(t, strings.Replace(heldAcross, "%s", "", 1), rate, 3)
		n := min(len(flat.left), len(offline.left))
		if p, _ := driftDelta(t, driftAudio{flat.left[:n], flat.right[:n]}, driftAudio{offline.left[:n], offline.right[:n]}); p == 0 {
			t.Fatalf("rate %d: the tempo step did not change the render", rate)
		}
	}
}

// A slide into the next scene's pattern releases the old note after the bar
// line, so the release is placed with the new tempo.
func TestSceneTempoSlideIntoNextSceneMatchesKernelSampleForSample(t *testing.T) {
	source := "tempo 120\ntrack bass acid {}\npattern p1 acid steps=16 { " + strings.Repeat(". ", 15) + "1~ }\npattern p2 acid steps=16 { 5 " + strings.Repeat(". ", 15) + "}\nscene a { bass = p1 }\nscene b { bass = p2 tempo = 60 }\nsong { a b }\n"
	for _, rate := range []int{44100, 48000} {
		offline, native := renderBoth(t, source, rate, 2)
		peak, _ := driftDelta(t, offline, native)
		if peak != 0 {
			t.Fatalf("rate %d: offline minus kernel peak %.9f", rate, peak)
		}
	}
}

// Authored tempos just around a rounding boundary become one canonical
// milli-BPM value, and the engine, the tempo map and the offline render agree
// on it to the frame.
func TestSceneTempoRoundingBoundariesAgreeEverywhere(t *testing.T) {
	for _, text := range []string{"20.0004999", "20.0005", "20.0005001", "96.0004999", "96.0005", "96.0005001", "299.9995", "299.9994999"} {
		t.Run(text, func(t *testing.T) {
			source := "tempo 120\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene a { bass = riff }\nscene b { bass = riff tempo = " + text + " }\nsong { a*2 b*3 }\n"
			score, diagnostics := notation.Parse([]byte(source))
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			p, ds := project.FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			milli, ok := project.SceneTempoMilli(p.Scenes[1])
			if !ok {
				t.Fatal("no scene tempo")
			}
			stored := *p.Scenes[1].Settings[0].Value.Number
			if stored != float64(milli)/1000 {
				t.Fatalf("the project keeps %v, want the canonical %v", stored, float64(milli)/1000)
			}
			const rate = 48_000
			tempo, err := project.NewTempoMap(rate, int64(p.TempoMilli), project.SongTempoChanges(p))
			if err != nil {
				t.Fatal(err)
			}
			want := tempo.SampleAtTick(5 * seq.TicksPerBar)
			cfg, err := project.CompileEngine(p, rate, 128)
			if err != nil {
				t.Fatal(err)
			}
			player, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := player.TempoAtTick(2 * seq.TicksPerBar); got != int64(milli) {
				t.Fatalf("the engine plays %d milli-BPM, the project says %d", got, milli)
			}
			if got := player.FramesBetweenTicks(0, 5*seq.TicksPerBar); got != want {
				t.Fatalf("engine frames %d, tempo map %d", got, want)
			}
			// The engine's transport, not only its map, reaches bar 5 there.
			player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			var l, r [128]float32
			reached := int64(-1)
			for frame := int64(0); frame < want+256 && reached < 0; frame += 128 {
				player.Render(l[:], r[:])
				if player.TransportSample() >= want && player.LiveClock().TickAtSample(want) >= 5*seq.TicksPerBar {
					reached = want
				}
			}
			if live := player.LiveClock(); live.BPMMilli != int64(milli) {
				t.Fatalf("the transport plays %d milli-BPM", live.BPMMilli)
			}
			var wav bytes.Buffer
			noDither := false
			report, err := WAV(score, Options{SampleRate: rate, TailSec: 0, Dither: &noDither}, &wav)
			if err != nil {
				t.Fatal(err)
			}
			if report.Frames != want {
				t.Fatalf("offline render %d frames, tempo map %d", report.Frames, want)
			}
		})
	}
}

// A valid 260-bar song that alternates 120 and 60 BPM has 259 tempo changes,
// more than one timing chunk records. An excerpt carries only the tempo at its
// first bar and the changes inside it, so it still verifies.
func TestSceneTempoExcerptOfALongSongVerifies(t *testing.T) {
	var song strings.Builder
	for i := 0; i < 130; i++ {
		song.WriteString(" a b")
	}
	source := "tempo 120\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene a { bass = riff tempo = 120 }\nscene b { bass = riff tempo = 60 }\nsong {" + song.String() + " }\n"
	score, diagnostics := notation.Parse([]byte(source))
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	if got := len(project.SongTempoChanges(p)); got != 259 {
		t.Fatalf("the song has %d tempo changes", got)
	}
	// Bars 102 and 103 (zero-based 101 and 102): 60 BPM, then 120 BPM.
	if got := project.ExcerptTempoChanges(p.TempoMilli, project.SongTempoChanges(p), 101, 2); len(got) != 2 || got[0] != (project.TempoChange{Bar: 101, BPMMilli: 60_000}) || got[1] != (project.TempoChange{Bar: 102, BPMMilli: 120_000}) {
		t.Fatalf("excerpt state = %+v", got)
	}
	noDither := false
	opts := Options{SampleRate: 48_000, From: 101, Bars: 2, TailSec: .1, Dither: &noDither}
	var wav bytes.Buffer
	report, err := WAV(score, opts, &wav)
	if err != nil {
		t.Fatal(err)
	}
	// 192000 frames at 60 BPM, then 96000 at 120 BPM, then the tail.
	if report.Frames != 192_000+96_000+4_800 {
		t.Fatalf("excerpt frames = %d", report.Frames)
	}
	path := filepath.Join(t.TempDir(), "excerpt.wav")
	if err := os.WriteFile(path, wav.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, From: 101, Bars: 2, TailSec: .1, PeakMaxDB: 0, DCMaxDB: 0}); err != nil {
		t.Fatalf("verify-wav: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "stems")
	stems, err := Stems(score, opts, dir)
	if err != nil {
		t.Fatal(err)
	}
	if verified, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil || verified.Frames != stems.Frames {
		t.Fatalf("verify-stems: %+v, %v", verified, err)
	}
}

// Both export routes read the song's tempo steps: the plain renderer and the
// scheduled one that scores with track inserts, audio clips or packed samplers
// take. A 120 then 60 BPM song is 6 s long on either route.
func TestSceneTempoScheduledRouteKeepsTheWholeSong(t *testing.T) {
	const insert = "fx hp eq { type = highpass frequency = 80Hz q = 0.707 }\n"
	for _, route := range []struct{ name, fx, track string }{
		{"plain", "", "track lead acid {}\n"},
		{"scheduled with an EQ insert", insert, "track lead acid { insert = hp }\n"},
	} {
		t.Run(route.name, func(t *testing.T) {
			source := "cicada 2\ntempo 120\n" + route.fx + route.track + "pattern line acid { 1 . 3 . 5 . 3 . }\nscene a { lead = line }\nscene b { lead = line tempo = 60 }\nsong { a b }\n"
			score, diagnostics := notation.Parse([]byte(source))
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			noDither := false
			opts := Options{SampleRate: 48_000, TailSec: .1, Dither: &noDither}
			var wav bytes.Buffer
			report, err := WAV(score, opts, &wav)
			if err != nil {
				t.Fatal(err)
			}
			if want := int64(96_000 + 192_000 + 4_800); report.Frames != want {
				t.Fatalf("exported %d frames, want %d (6 s plus the tail)", report.Frames, want)
			}
			chunk := wav.Bytes()[44+report.Frames*6:]
			if binary.LittleEndian.Uint32(chunk[8:12]) != 2 || binary.LittleEndian.Uint32(chunk[4:8]) != 16+8 {
				t.Fatalf("timing chunk: % x", chunk)
			}
			path := filepath.Join(t.TempDir(), "song.wav")
			if err := os.WriteFile(path, wav.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 2, TailSec: .1, PeakMaxDB: 0, DCMaxDB: 0}); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "stems")
			stems, err := Stems(score, opts, dir)
			if err != nil {
				t.Fatal(err)
			}
			if stems.Frames != report.Frames {
				t.Fatalf("stems are %d frames, the WAV %d", stems.Frames, report.Frames)
			}
			if _, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// An excerpt that starts after the song has returned to the score tempo still
// records its start state, so the length verifies on both routes.
func TestSceneTempoExcerptAfterReturningToTheScoreTempoVerifies(t *testing.T) {
	cases := []struct {
		name          string
		score, middle int
		song          string
		from, bars    int
	}{
		{"124 to 60 to 124", 124, 60, "a b c*4", 4, 1},
		{"121 to 60 to 121", 121, 60, "a b c*3", 2, 1},
	}
	const insert = "fx hp eq { type = highpass frequency = 80Hz q = 0.707 }\n"
	for _, c := range cases {
		for _, route := range []struct{ name, fx, track string }{{"plain", "", "track lead acid {}\n"}, {"scheduled", insert, "track lead acid { insert = hp }\n"}} {
			t.Run(c.name+"/"+route.name, func(t *testing.T) {
				source := fmt.Sprintf("cicada 2\ntempo %d\n%s%spattern line acid { 1 . 3 . 5 . 3 . }\nscene a { lead = line }\nscene b { lead = line tempo = %d }\nscene c { lead = line tempo = %d }\nsong { %s }\n", c.score, route.fx, route.track, c.middle, c.score, c.song)
				score, diagnostics := notation.Parse([]byte(source))
				if len(diagnostics) != 0 {
					t.Fatal(diagnostics)
				}
				noDither := false
				opts := Options{SampleRate: 48_000, From: c.from, Bars: c.bars, TailSec: .05, Dither: &noDither}
				var wav bytes.Buffer
				report, err := WAV(score, opts, &wav)
				if err != nil {
					t.Fatal(err)
				}
				chunk := wav.Bytes()[44+report.Frames*6:]
				if binary.LittleEndian.Uint32(chunk[8:12]) != 2 {
					t.Fatalf("an excerpt after tempo changes must record its start state: % x", chunk[:12])
				}
				path := filepath.Join(t.TempDir(), "excerpt.wav")
				if err := os.WriteFile(path, wav.Bytes(), 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, From: c.from, Bars: c.bars, TailSec: .05, PeakMaxDB: 0, DCMaxDB: 0}); err != nil {
					t.Fatalf("verify-wav: %v", err)
				}
				dir := filepath.Join(t.TempDir(), "stems")
				if _, err := Stems(score, opts, dir); err != nil {
					t.Fatal(err)
				}
				if _, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
					t.Fatalf("verify-stems: %v", err)
				}
			})
		}
	}
}

// A scene that steps the tempo and the delay division together fades the delay
// once. The offline render and the kernel agree sample for sample, at both
// common sample rates, in either order the settings are written.
func TestSceneTempoWithDelayDivisionMatchesKernelSampleForSample(t *testing.T) {
	for _, settings := range []string{"tempo = 60 delay.time = 1/4", "delay.time = 1/4 tempo = 60"} {
		source := "tempo 120\nfx delay delay { time = 1/8 feedback = 0.3 damp = 6kHz mix = 1 }\ntrack bass acid { send delay = 0.8 }\npattern p acid steps=16 { 1 . . . 5 . . . 1 . . . 3 . . . }\nscene a { bass = p }\nscene b { bass = p " + settings + " }\nsong { a b }\n"
		for _, rate := range []int{44100, 48000} {
			offline, native := renderBoth(t, source, rate, 2)
			if peak, _ := driftDelta(t, offline, native); peak != 0 {
				t.Fatalf("%q at %d Hz: offline minus kernel peak %.9f", settings, rate, peak)
			}
		}
	}
}
