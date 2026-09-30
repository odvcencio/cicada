package notation

import (
	"bytes"
	"strings"
	"testing"
)

const audioTestScore = `asset vocal "audio/vocal.wav" {
 sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
 format = wav
 frames = 4800
 rate = 48000Hz
 channels = 1
 source = generated
}
clip vocal-a vocal { start = 10ms end = 0.1s gain = -2dB fade_in = 48frames fade_out = 2ms }
sampler hit { asset = vocal root = c3 mode = oneshot voices = 8 }
track chops hit {}
track vox audio {}
pattern hits { c3 . c3 . }
scene verse { chops = hits vox = vocal-a }
song { verse*4 }
`

func TestAssetDeclParses(t *testing.T) {
	score, ds := ParseEdition([]byte(audioTestScore), 2)
	if parseHasErrors(ds) {
		t.Fatalf("%+v", ds)
	}
	a := score.Assets[0]
	c := score.Clips[0]
	v := score.Samplers[0]
	if a.Name != "vocal" || a.Path != "audio/vocal.wav" || a.Format != "wav" || a.Frames != 4800 || a.RateHz != 48000 || a.Channels != 1 || c.StartFrame != 480 || c.EndFrame != 4800 || c.FadeInFrames != 48 || c.FadeOutFrames != 96 || c.GainDB != -2 || v.RootMIDI != 48 || v.Voices != 8 || v.Mode != "oneshot" {
		t.Fatalf("asset=%+v clip=%+v sampler=%+v", a, c, v)
	}
	_, ds = ParseEdition([]byte(audioTestScore), 1)
	requireAudioCode(t, ds, "CICADA-VERSION")
}
func requireAudioCode(t *testing.T, ds []Diagnostic, code string) {
	t.Helper()
	for _, d := range ds {
		if d.Code == code && d.Severity == "error" && d.Position.Line > 0 && d.Position.Column > 0 && strings.Contains(d.Message, "expected") && strings.Contains(d.Message, "actual") {
			return
		}
	}
	t.Fatalf("missing %s: %+v", code, ds)
}
func TestClipRangeDiagnostic(t *testing.T) {
	for _, replacement := range []string{"end = 4801frames", "end = 0frames", "end = 0.00001s", "end = -1ms", "end = 999999999999999999999999frames"} {
		t.Run(replacement, func(t *testing.T) {
			_, ds := ParseEdition([]byte(strings.Replace(audioTestScore, "end = 0.1s", replacement, 1)), 2)
			requireAudioCode(t, ds, "CICADA-CLIP-RANGE")
		})
	}
	score, ds := ParseEdition([]byte(strings.Replace(audioTestScore, "clip vocal-a vocal { start = 10ms end = 0.1s gain = -2dB fade_in = 48frames fade_out = 2ms }", "clip vocal-a vocal {}", 1)), 2)
	if parseHasErrors(ds) || score.Clips[0].EndFrame != 4800 {
		t.Fatalf("default region: %+v %+v", score, ds)
	}
}
func TestSamplerParamsValidate(t *testing.T) {
	for _, change := range [][2]string{{"voices = 8", "voices = 0"}, {"voices = 8", "voices = 33"}, {"voices = 8", "voices = 1.5"}, {"root = c3", "root = c"}, {"root = c3", "root = nonsense"}, {"mode = oneshot", "mode = stretch"}, {"voices = 8", "voics = 8"}} {
		_, ds := ParseEdition([]byte(strings.Replace(audioTestScore, change[0], change[1], 1)), 2)
		requireAudioCode(t, ds, "CICADA-SAMPLER-PARAM")
	}
	for _, mode := range []string{"oneshot", "loop"} {
		_, ds := ParseEdition([]byte(strings.Replace(audioTestScore, "mode = oneshot", "mode = "+mode, 1)), 2)
		if parseHasErrors(ds) {
			t.Fatalf("%s: %+v", mode, ds)
		}
	}
	_, ds := ParseEdition([]byte(strings.Replace(audioTestScore, "asset = vocal", "asset = vocla", 1)), 2)
	requireAudioCode(t, ds, "CICADA-REFERENCE")
	found := false
	for _, d := range ds {
		found = found || strings.Contains(d.Message, `did you mean "vocal"?`)
	}
	if !found {
		t.Fatalf("spelling suggestion: %+v", ds)
	}
}
func TestAssetsRoundTripThroughFmt(t *testing.T) {
	source := []byte("// assets\n" + strings.Replace(audioTestScore, "format = wav", "// PCM source\n format = wav", 1))
	d, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(d)
	if err != nil {
		t.Fatal(err)
	}
	d, err = ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format(d)
	if err != nil || !bytes.Equal(formatted, again) {
		t.Fatalf("not idempotent: %v\n%s\n%s", err, formatted, again)
	}
	if !bytes.Contains(formatted, []byte("// assets")) || !bytes.Contains(formatted, []byte("// PCM source")) {
		t.Fatal("lost comment")
	}
	before, ds := ParseEdition(source, 2)
	if parseHasErrors(ds) {
		t.Fatal(ds)
	}
	after, ds := ParseEdition(formatted, 2)
	if parseHasErrors(ds) {
		t.Fatal(ds)
	}
	if before.Assets[0].SHA256 != after.Assets[0].SHA256 || before.Clips[0].EndFrame != after.Clips[0].EndFrame || before.Samplers[0].RootMIDI != after.Samplers[0].RootMIDI {
		t.Fatal("fmt changed audio data")
	}
}
