package render

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// The decay cost survey measures, per voice family and per effect, how much
// CPU each second of audio takes while a note rings and after it ends. A cost
// that grows as a sound decays points at state stuck in subnormal numbers; a
// cost that stays high in silence points at work an idle track does not need.
// It is a measurement, not a gate: set CICADA_DECAY_COST=1 to run it.

// surveyVoices are the polyphonic voices the survey holds a chord on.
var surveyVoices = []string{
	"tine_ep", "tine_bell", "tine_bark", "tine_tremolo", "reed_ep", "reed_tremolo",
	"fm_ep", "fm_bass", "bell_keys", "poly_keys", "soft_pad", "string_machine", "sync_lead", "brass_stab",
	"clav", "clav_muted", "clav_hollow", "organ_full", "organ_jazz", "organ_soft", "tonewheel_organ",
	"piano",
}

// surveyMonoVoices strike one note and let it ring out.
var surveyMonoVoices = []string{"acid", "model_wood", "model_zinc", "model_marble", "model_felt"}

// surveyKits strike one bass drum and snare hit and let them ring out.
var surveyKits = []string{"acoustic", "drums", "drums.steel", "drums.circuit"}

// struckSurveyScore strikes once in its first second and is silent after,
// for seconds seconds in all. Extra goes before the track (effects) and
// trackExtra inside its braces.
func struckSurveyScore(voice, hit, extra, trackExtra string, seconds int) string {
	return fmt.Sprintf("cicada 2\ntempo 240\nkey c major\nseed 1\n%s\ntrack t %s { %s }\n%s\nscene a { t = hit }\nscene b { t = rest }\nsong { a b*%d }\n", extra, voice, trackExtra, hit, seconds-1)
}

const (
	noteHit = "pattern hit notes {\n  step = 1/1\n  gate = 50%\n  c3\n}\npattern rest notes {\n  step = 1/1\n  .\n}"
	kitHit  = "pattern hit drums {\n  bd: x... .... .... ....\n  sd: .... x... .... ....\n}\npattern rest drums {\n  bd: .... .... .... ....\n}"
)

func TestStruckCostSurvey(t *testing.T) {
	if os.Getenv("CICADA_DECAY_COST") != "1" {
		t.Skip("set CICADA_DECAY_COST=1 to measure")
	}
	runtime.GOMAXPROCS(1)
	t.Logf("METRIC STRUCK-COST columns: ms of CPU per second of audio at seconds 1, 2, 8, 16, 30 after one strike")
	report := func(name, source string) {
		t.Run(name, func(t *testing.T) {
			c := engineSecondCosts(t, source, 30)
			t.Logf("METRIC STRUCK-COST %s %.1f/%.1f/%.1f/%.1f/%.1f", name, ms(c[0]), ms(c[1]), ms(c[7]), ms(c[15]), ms(c[29]))
		})
	}
	for _, voice := range surveyMonoVoices {
		report("voice="+voice, struckSurveyScore(voice, noteHit, "", "", 30))
	}
	for _, kit := range surveyKits {
		report("kit="+kit, struckSurveyScore(kit, kitHit, "", "", 30))
	}
	// Effects after a burst: a clean voice (the organ stops at its release)
	// feeding each effect, then 29 seconds of silence.
	for _, effect := range []struct{ name, extra, track string }{
		{"none", "", ""},
		{"reverb", "fx room reverb {}", "send room = 0.5"},
		{"delay", "fx echo delay {}", "send echo = 0.5"},
		{"drive", "fx hot drive { gain = 9dB }", "insert = hot"},
		{"eq", "fx tone eq { type = highpass frequency = 40Hz }", "insert = tone"},
		{"comp", "fx glue comp { threshold = -18dB }", "insert = glue"},
		{"master chain", "fx tone eq { type = highpass frequency = 25Hz }\nfx glue comp { threshold = -18dB }\nfx ceiling limiter { ceiling = -1dBTP }\nmaster { insert = tone -> glue -> ceiling }", ""},
	} {
		report("effect="+effect.name, struckSurveyScore("organ_soft", noteHit, effect.extra, effect.track, 30))
	}
}

// heldSurveyScore holds a C major seventh chord for held seconds, then rests
// for rest seconds, at one bar per second.
func heldSurveyScore(voice string, held, rest int) string {
	steps := []string{"[c4 e4 g4 b4]"}
	for range held - 1 {
		steps = append(steps, "-")
	}
	for range rest {
		steps = append(steps, ".")
	}
	return fmt.Sprintf("cicada 2\ntempo 240\nkey c major\nseed 1\ntrack t %s {}\npattern p notes {\n  step = 1/1\n  gate = 100%%\n  %s\n}\nscene a { t = p }\nsong { a*%d }\n", voice, strings.Join(steps, " "), held+rest)
}

// engineSecondCosts renders a score on the engine and returns the wall time of
// each second of audio.
func engineSecondCosts(t *testing.T, source string, seconds int) []time.Duration {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Skipf("score does not parse: %v", d)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Skipf("score does not compile: %v", diagnostics)
	}
	const rate, block = 48000, 128
	cfg, err := project.CompileEngine(p, rate, block)
	if err != nil {
		t.Skipf("engine config: %v", err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Skipf("engine: %v", err)
	}
	defer e.Close()
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	left, right := make([]float32, block), make([]float32, block)
	costs := make([]time.Duration, seconds)
	for s := range costs {
		started := time.Now()
		for range rate / block {
			e.Render(left, right)
		}
		costs[s] = time.Since(started)
	}
	return costs
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func TestDecayCostSurvey(t *testing.T) {
	if os.Getenv("CICADA_DECAY_COST") != "1" {
		t.Skip("set CICADA_DECAY_COST=1 to measure")
	}
	runtime.GOMAXPROCS(1)
	t.Logf("METRIC DECAY-COST columns: ms of CPU per second of audio at seconds 1, 8, 16 (chord held) and 17, 24, 30 (released)")
	for _, voice := range surveyVoices {
		t.Run(voice, func(t *testing.T) {
			c := engineSecondCosts(t, heldSurveyScore(voice, 16, 14), 30)
			t.Logf("METRIC DECAY-COST voice=%s held=%.1f/%.1f/%.1f released=%.1f/%.1f/%.1f growth=%.1fx", voice, ms(c[0]), ms(c[7]), ms(c[15]), ms(c[16]), ms(c[23]), ms(c[29]), ms(c[15])/max(ms(c[0]), .001))
		})
	}
}

// TestSilentSongCost measures a song whose tracks are all off: the large
// reference song with one scene that turns every track off.
func TestSilentSongCost(t *testing.T) {
	if os.Getenv("CICADA_DECAY_COST") != "1" {
		t.Skip("set CICADA_DECAY_COST=1 to measure")
	}
	runtime.GOMAXPROCS(1)
	source := silentLargeSong(t)
	c := engineSecondCosts(t, source, 16)
	var total time.Duration
	for _, d := range c {
		total += d
	}
	t.Logf("METRIC SILENT-SONG engine ms per audio second: first %.1f, mean %.1f, last %.1f", ms(c[0]), ms(total)/float64(len(c)), ms(c[len(c)-1]))
}

// silentLargeSong is examples/songs/anywhere-you-are.cicada without its
// automation, playing one scene that turns every track off for 32 bars.
func silentLargeSong(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "examples", "songs", "anywhere-you-are.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	return silentVariant(string(data), 32)
}

var (
	automateBlock = regexp.MustCompile(`(?ms)^automate .*?^\}\n?`)
	trackName     = regexp.MustCompile(`(?m)^track (\S+) `)
	songBlock     = regexp.MustCompile(`(?ms)^song \{.*?^\}`)
)

// silentVariant replaces a score's song with one scene, played for bars bars,
// that turns every track off, and drops its automation, which names scenes
// that the variant no longer plays.
func silentVariant(source string, bars int) string {
	source = automateBlock.ReplaceAllString(source, "")
	var scene strings.Builder
	scene.WriteString("scene silent {\n")
	for _, match := range trackName.FindAllStringSubmatch(source, -1) {
		scene.WriteString("  " + match[1] + " = off\n")
	}
	scene.WriteString("}\n\n")
	return songBlock.ReplaceAllString(source, scene.String()+fmt.Sprintf("song {\n  silent*%d\n}", bars))
}

// directSecondCost is the direct renderer's cost of second s (one-based) of a
// score at one bar per second: a render of s bars minus a render of s-1 bars.
func directSecondCost(t *testing.T, source string, s int) time.Duration {
	t.Helper()
	score, _ := notation.Parse([]byte(source))
	timeBars := func(bars int) time.Duration {
		if bars == 0 {
			return 0
		}
		started := time.Now()
		if _, err := WAV(score, Options{SampleRate: 48000, Bits: 24, Bars: bars, TailSec: 0}, io.Discard); err != nil {
			t.Fatal(err)
		}
		return time.Since(started)
	}
	return timeBars(s) - timeBars(s-1)
}

func TestDecayCostDirect(t *testing.T) {
	if os.Getenv("CICADA_DECAY_COST") != "1" {
		t.Skip("set CICADA_DECAY_COST=1 to measure")
	}
	runtime.GOMAXPROCS(1)
	voices := strings.Split(os.Getenv("CICADA_DECAY_VOICES"), ",")
	if voices[0] == "" {
		voices = []string{"tine_ep", "piano"}
	}
	for _, voice := range voices {
		t.Run(voice, func(t *testing.T) {
			source := heldSurveyScore(voice, 16, 14)
			score, _ := notation.Parse([]byte(source))
			t.Logf("METRIC DECAY-COST-DIRECT voice=%s scheduled=%v second1=%.1f second8=%.1f second16=%.1f second24=%.1f", voice, UsesScheduledEngine(score, Options{}), ms(directSecondCost(t, source, 1)), ms(directSecondCost(t, source, 8)), ms(directSecondCost(t, source, 16)), ms(directSecondCost(t, source, 24)))
		})
	}
}
