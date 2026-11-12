package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func audioRenderFixture(t *testing.T, withSampler bool) (string, *notation.Score) {
	t.Helper()
	dir := t.TempDir()
	data := testwav.Bytes(48000, 2, 32, 120000, 3)
	for i := 0; i < 120000; i++ {
		sample := float32(math.Sin(float64(i)*.09) * .2)
		binary.LittleEndian.PutUint32(data[44+i*8:], math.Float32bits(sample))
		binary.LittleEndian.PutUint32(data[48+i*8:], math.Float32bits(-sample*.3))
	}
	if err := os.WriteFile(filepath.Join(dir, "take.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("cicada 2\ntempo 120\nasset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 120000 rate = 48000Hz channels = 2 }\nclip take-a take { start = 100frames end = 119000frames gain = -3dB fade_in = 100frames fade_out = 200frames }\ntrack vox audio {}\nscene main { vox = take-a }\nscene hold { vox = keep }\nscene quiet { vox = off }\nsong { main hold quiet main }\n", sha256.Sum256(data))
	if withSampler {
		text += "sampler hit { asset = take root = c3 mode = loop voices = 2 }\ntrack keys hit {}\npattern notes { c3 . g3 . }\nscene keys { keys=notes vox=off }\n"
		text = string(bytes.Replace([]byte(text), []byte("song { main hold quiet main }"), []byte("song { main hold quiet keys }"), 1))
	}
	score, ds := notation.Parse([]byte(text))
	if score == nil || hasAudioErrors(ds) {
		t.Fatal(ds)
	}
	return dir, score
}
func hasAudioErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

func TestNativeAudioMatchesWAVAndRange(t *testing.T) {
	dir, score := audioRenderFixture(t, false)
	checkNativeAudioMatchesWAVAndRange(t, dir, score)
}

func TestAudioSceneAutomationMatchesNativeWAVAndRange(t *testing.T) {
	dir, score := audioRenderFixture(t, false)
	score.Scenes[0].Settings = []notation.SceneSetting{{Path: "vox.level", Value: "-12.25dB"}, {Path: "vox.pan", Value: "-0.375"}}
	score.Scenes[1].Settings = []notation.SceneSetting{{Path: "vox.level", Value: "-3.75dB"}, {Path: "vox.pan", Value: "0.625"}}
	checkNativeAudioMatchesWAVAndRange(t, dir, score)
}

func checkNativeAudioMatchesWAVAndRange(t *testing.T, dir string, score *notation.Score) {
	t.Helper()
	var wav, small, ranged bytes.Buffer
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Block: 128}
	report, err := WAV(score, opts, &wav)
	if err != nil {
		t.Fatal(err)
	}
	opts.Block = 31
	if _, err := WAV(score, opts, &small); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wav.Bytes(), small.Bytes()) {
		t.Fatal("audio WAV depends on block size")
	}
	opts.From, opts.Bars = 1, 1
	if _, err := WAV(score, opts, &ranged); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ranged.Bytes()[44:44+96000*8], wav.Bytes()[44+96000*8:44+192000*8]) {
		t.Fatal("audio range does not match full-song playback")
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := sampleasset.CompileEngine(dir, p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	limiter, _ := mix.NewLimiter(48000)
	latency := limiter.LatencyFrames()
	l, r := make([]float32, int(report.Frames)+latency), make([]float32, int(report.Frames)+latency)
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	for at := 0; at < len(l); at += 128 {
		end := min(len(l), at+128)
		e.Render(l[at:end], r[at:end])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("fault: %+v", m)
			}
		}
	}
	pcm := wav.Bytes()[44 : 44+int(report.Frames)*8]
	for i := 0; i < int(report.Frames); i++ {
		wl := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*8:]))
		wr := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*8+4:]))
		if math.Abs(float64(wl-l[i+latency])) > 1e-7 || math.Abs(float64(wr-r[i+latency])) > 1e-7 {
			t.Fatalf("native/WAV mismatch frame %d: %v/%v vs %v/%v", i, l[i+latency], r[i+latency], wl, wr)
		}
	}
}

func TestSamplerWAVAndImmutableAssetFailure(t *testing.T) {
	dir, score := audioRenderFixture(t, true)
	var wav bytes.Buffer
	report, err := WAV(score, Options{AssetDir: dir, SampleRate: 48000, Bits: 32}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	if report.OutputPeak <= 0 || report.ClippedSamples != 0 {
		t.Fatalf("sampler output: %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	data[44] ^= 1
	os.WriteFile(filepath.Join(dir, "take.wav"), data, 0600)
	wav.Reset()
	if _, err := WAV(score, Options{AssetDir: dir}, &wav); err == nil || wav.Len() != 0 {
		t.Fatal("changed asset was rendered or partially published")
	}
	if _, err := WAV(score, Options{}, &wav); err == nil {
		t.Fatal("audio render implicitly trusted the current directory")
	}
}

func TestRepeatedAudioSceneRetriggersAndKeepContinues(t *testing.T) {
	dir, score := audioRenderFixture(t, false)
	score.Song = []notation.SongEntry{{Scene: "main", Bars: 1}, {Scene: "main", Bars: 1}}
	var repeated, held bytes.Buffer
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Block: 128}
	if _, err := WAV(score, opts, &repeated); err != nil {
		t.Fatal(err)
	}
	score.Song[1].Scene = "hold"
	if _, err := WAV(score, opts, &held); err != nil {
		t.Fatal(err)
	}
	// Allow the bounded switch declick to finish before comparing the phrase.
	first := repeated.Bytes()[44+512*8 : 44+1024*8]
	second := repeated.Bytes()[44+(96000+512)*8 : 44+(96000+1024)*8]
	if !bytes.Equal(first, second) {
		t.Fatal("explicit scene assignment did not retrigger the clip")
	}
	if bytes.Equal(first, held.Bytes()[44+(96000+512)*8:44+(96000+1024)*8]) {
		t.Fatal("keep retriggered the clip")
	}
}

// A sampler chord on the direct WAV mixer starts every note, as the engine does.
func TestDirectMixerSamplerChordPlaysEveryNote(t *testing.T) {
	dir, _ := audioRenderFixture(t, false)
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	build := func(step string) *notation.Score {
		text := fmt.Sprintf("cicada 2\ntempo 120\nasset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 120000 rate = 48000Hz channels = 2 }\nsampler hit { asset = take root = c4 mode = loop voices = 8 }\ntrack keys hit {}\npattern ch notes { %s - - - }\nscene a { keys = ch }\nsong { a }\n", sha256.Sum256(data), step)
		score, ds := notation.Parse([]byte(text))
		if score == nil || hasAudioErrors(ds) {
			t.Fatal(ds)
		}
		return score
	}
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Block: 128}
	render := func(score *notation.Score) ([]byte, Report) {
		if UsesScheduledEngine(score, opts) {
			t.Fatal("fixture must use the direct mixer")
		}
		var wav bytes.Buffer
		report, err := WAV(score, opts, &wav)
		if err != nil {
			t.Fatal(err)
		}
		return wav.Bytes(), report
	}
	root, _ := render(build("c4"))
	chordScore := build("[c4 e4 g4]")
	chord, report := render(chordScore)
	if bytes.Equal(root, chord) {
		t.Fatal("sampler chord rendered like its root")
	}
	p, ds := project.FromScore(chordScore)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := sampleasset.CompileEngine(dir, p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	limiter, _ := mix.NewLimiter(48000)
	latency := limiter.LatencyFrames()
	l, r := make([]float32, int(report.Frames)+latency), make([]float32, int(report.Frames)+latency)
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	for at := 0; at < len(l); at += 128 {
		end := min(len(l), at+128)
		e.Render(l[at:end], r[at:end])
	}
	var worst float64
	for i := 0; i < int(report.Frames); i++ {
		wl := math.Float32frombits(binary.LittleEndian.Uint32(chord[44+i*8:]))
		wr := math.Float32frombits(binary.LittleEndian.Uint32(chord[48+i*8:]))
		worst = math.Max(worst, math.Max(math.Abs(float64(wl-l[i+latency])), math.Abs(float64(wr-r[i+latency]))))
	}
	if worst > 1e-6 {
		t.Fatalf("direct chord differs from the engine by %g", worst)
	}
}

// A single-asset sampler plays bend, vibrato and pressure rows
// (docs/manual/engine-host.md). Only the engine implements them, so such a
// score renders there: the direct mixer used to render a bend row as if it
// were absent, while Studio played it.
func TestSamplerExpressionRowsPlayOnRender(t *testing.T) {
	dir, _ := audioRenderFixture(t, false)
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	build := func(row string) *notation.Score {
		text := fmt.Sprintf("cicada 2\ntempo 120\nasset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 120000 rate = 48000Hz channels = 2 }\nsampler hit { asset = take root = c4 mode = loop voices = 8 }\ntrack keys hit {}\npattern ch notes { c4 d4 e4 g4%s }\nscene a { keys = ch }\nsong { a }\n", sha256.Sum256(data), row)
		score, ds := notation.Parse([]byte(text))
		if score == nil || hasAudioErrors(ds) {
			t.Fatal(ds)
		}
		return score
	}
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Block: 128}
	render := func(score *notation.Score) []byte {
		var wav bytes.Buffer
		if _, err := WAV(score, opts, &wav); err != nil {
			t.Fatal(err)
		}
		return wav.Bytes()
	}
	plain, bent := build(""), build("\n bend: 200ct 0ct 200ct 0ct")
	if UsesScheduledEngine(plain, opts) {
		t.Fatal("a sampler without rows should keep the direct mixer")
	}
	if !UsesScheduledEngine(bent, opts) {
		t.Fatal("a sampler bend row must render through the engine")
	}
	if bytes.Equal(render(plain), render(bent)) {
		t.Fatal("the bend row rendered the same as no row")
	}
}

// Only a sampler track's own rows need the engine. Rows on a synth track
// beside a sampler keep the direct mixer, which plays them; rows that a
// sampler track plays through its chain route to the engine.
func TestSamplerExpressionRoutingLooksAtSamplerTracks(t *testing.T) {
	dir, _ := audioRenderFixture(t, false)
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	head := fmt.Sprintf("cicada 2\ntempo 120\nasset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 120000 rate = 48000Hz channels = 2 }\nsampler hit { asset = take root = c4 mode = loop voices = 8 }\n", sha256.Sum256(data))
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Block: 128}
	for _, c := range []struct {
		name, body string
		engine     bool
	}{
		{"synth rows beside a sampler", "track keys hit {}\ntrack lead acid {}\npattern ch notes { c4 d4 e4 g4 }\npattern riff notes { 1 3 5 1\n bend: 100ct 0ct 100ct 0ct }\nscene a { keys = ch lead = riff }\nsong { a }\n", false},
		{"sampler rows through a chain", "track keys hit { chain = bent }\npattern bent notes { c4 d4 e4 g4\n bend: 200ct 0ct 200ct 0ct }\nscene a {}\nsong { a }\n", true},
	} {
		score, ds := notation.Parse([]byte(head + c.body))
		if score == nil || hasAudioErrors(ds) {
			t.Fatalf("%s: %v", c.name, ds)
		}
		if got := UsesScheduledEngine(score, opts); got != c.engine {
			t.Errorf("%s: scheduled engine %v, want %v", c.name, got, c.engine)
		}
	}
}
