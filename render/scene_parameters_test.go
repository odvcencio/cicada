package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func assertSceneEngineMatchesWAV(t *testing.T, score *notation.Score, wav []byte, rate, first, last int) {
	t.Helper()
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project diagnostics: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, rate, 128)
	if err != nil {
		t.Fatal(err)
	}
	live, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !live.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play command rejected")
	}
	limiter, err := mix.NewLimiter(rate)
	if err != nil {
		t.Fatal(err)
	}
	latency := limiter.LatencyFrames()
	for i := 0; i < cfg.Tracks; i++ {
		if cfg.Track[i].InsertDrive != nil {
			latency += fx.DriveLatencyFrames
			break
		}
	}
	var left, right [128]float32
	var peakDifference float64
	var peakFrame, peakChannel int
	for position := 0; position < last+latency; position += len(left) {
		live.Render(left[:], right[:])
		var message cmd.Message
		for live.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine fault: %+v", message)
			}
		}
		for i := range left {
			frame := position + i - latency
			if frame < first || frame >= last {
				continue
			}
			for channel, value := range [...]float32{left[i], right[i]} {
				offset := 44 + frame*8 + channel*4
				offline := math.Float32frombits(binary.LittleEndian.Uint32(wav[offset : offset+4]))
				delta := math.Abs(float64(value) - float64(offline))
				if math.IsNaN(delta) || math.IsInf(delta, 0) {
					t.Fatalf("non-finite sample at frame %d channel %d", frame, channel)
				}
				if delta > peakDifference {
					peakDifference, peakFrame, peakChannel = delta, frame, channel
				}
			}
		}
	}
	t.Logf("offline/engine peak sample difference %.9g at frame %d channel %d (tolerance 1e-6)", peakDifference, peakFrame, peakChannel)
	if peakDifference > 1e-6 {
		t.Errorf("offline/engine peak sample difference %.9g exceeds 1e-6", peakDifference)
	}
}

func renderSceneScore(t *testing.T, source string, opts Options) (*notation.Score, []byte, Report) {
	t.Helper()
	score, ds := notation.ParseEdition([]byte(source), 2)
	if len(ds) != 0 {
		t.Fatalf("score diagnostics: %+v", ds)
	}
	var wav bytes.Buffer
	report, err := WAV(score, opts, &wav)
	if err != nil {
		t.Fatal(err)
	}
	return score, wav.Bytes(), report
}

func TestOfflineSceneParameterParity(t *testing.T) {
	const base = `tempo 120
key a minor
seed 42
fx drive drive {}
fx delay delay {}
fx reverb reverb {}
fx comp comp {}
bus music { insert = comp }
track bass acid { cutoff = 700Hz insert = drive send delay = 0 pre send reverb = 0 }
track drums drums {}
pattern riff acid steps=4 { 1 . 5 . }
pattern beat drums steps=4 { bd: x . x . ch: x x x x }
scene first { bass = riff drums = beat }
scene second { bass = riff drums = beat SETTINGS }
scene third { bass = keep drums = keep }
song { first second*2 third }
`
	for _, tc := range []struct{ name, settings string }{
		{"acid order", "bass.cutoff = 4000Hz bass.reso = 0.7 bass.envmod = 0.6 bass.decay = 700ms bass.accent = 0.9"},
		{"mixer", "bass.level = -12dB bass.pan = 0.35 bass.send.delay = 0.4 bass.send.reverb = 0.2"},
		{"mute", "bass.mute = true"},
		{"solo", "bass.solo = true"},
		{"source off", "bass.level = off"},
		{"drums", "drums.bd_tune = 90Hz drums.bd_decay = 600ms drums.bd_level = -12dB drums.bd_pan = 0.3 drums.ch_decay = 100ms drums.ch_level = off"},
		{"effects", "bass.send.delay = 0.4 bass.send.reverb = 0.2 drive.gain = 6dB drive.tone = 6000Hz drive.mix = 0.8 delay.time = 1/8 delay.feedback = 0.5 delay.damp = 4000Hz delay.pingpong = true delay.width = 0.8 delay.mix = 0.7 reverb.size = 0.7 reverb.decay = 1800ms reverb.damp = 5000Hz reverb.highpass = 200Hz reverb.predelay = 10ms reverb.mix = 0.5 comp.threshold = -15dB comp.ratio = 3 comp.knee = 4dB comp.attack = 8ms comp.release = 90ms comp.makeup = auto comp.mix = 0.8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rate := range []int{44_100, 48_000} {
				t.Run(strconv.Itoa(rate), func(t *testing.T) {
					score, wav, report := renderSceneScore(t, strings.Replace(base, "SETTINGS", tc.settings, 1), Options{SampleRate: rate, Bits: 32})
					assertSceneEngineMatchesWAV(t, score, wav, rate, 0, int(report.Frames))
				})
			}
		})
	}
}

func TestOfflineSceneParametersRangesStemsAndNormalization(t *testing.T) {
	const source = `tempo 120
key a minor
seed 42
track bass acid { cutoff = 700Hz }
pattern riff acid steps=4 { 1 . 5 . }
scene first { bass = riff }
scene second { bass = riff bass.cutoff = 4000Hz bass.pan = 0.3 }
scene third { bass = keep }
song { first second*2 third }
`
	opts := Options{SampleRate: 48_000, Bits: 32}
	score, full, report := renderSceneScore(t, source, opts)
	opts.Block = 64
	_, small, _ := renderSceneScore(t, source, opts)
	if !bytes.Equal(full, small) {
		t.Fatal("scene parameters depend on block size")
	}
	opts.From, opts.Bars = 2, 1
	_, part, partReport := renderSceneScore(t, source, opts)
	if !bytes.Equal(part[44:44+int(partReport.Frames)*8], full[44+2*96_000*8:44+3*96_000*8]) {
		t.Fatal("range lost earlier scene parameters")
	}
	dir := filepath.Join(t.TempDir(), "stems")
	if _, err := Stems(score, opts, dir); err != nil {
		t.Fatal(err)
	}
	master, err := os.ReadFile(filepath.Join(dir, "master.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(master, part) {
		t.Fatal("stems master differs from scene parameter WAV")
	}
	if _, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
		t.Fatal(err)
	}
	opts.From, opts.Bars, opts.Normalize = 0, 0, true
	_, _, normalized := renderSceneScore(t, source, opts)
	if math.Abs(amplitudeDB(float64(normalized.OutputPeak))+1) > .001 {
		t.Fatalf("normalized peak: %+v (unnormalized %+v)", normalized, report)
	}
}
