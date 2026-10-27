package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/sample"
)

// browserBandSource is a small band. The harp is a poly instrument: with a
// chord step placed it takes four voices, without one it takes eight.
const browserBandSource = `cicada 2
tempo 120
key c major
seed 7
instrument harp {
  octave = 4
  voice poly {
    let burst = noise() * env(gate, 1ms) * velocity
    let string = comb(burst, 1 / pitch, 0.996, 0.45)
    let amp = adsr(gate, 1ms, 1800ms, 0, 1500ms)
    out = string * amp * 0.9
  }
}
track lead acid {}
track vibes model_vibraphone {}
track harp harp {}
track drums drums {}
pattern riff acid { 1 . 5 . }
pattern tune notes steps=4 { c4 . e4 . }
pattern strum notes steps=4 { [c4 e4 g4] . . . }
pattern beat drums { bd: x... x... }
scene main { lead = riff vibes = tune harp = strum drums = beat }
song { main }
`

// vibraphoneSource declares tracks of the modeled vibraphone, which reserves
// four voices each.
func vibraphoneSource(tracks int) string {
	var s strings.Builder
	s.WriteString("cicada 2\ntempo 120\n")
	for i := 0; i < tracks; i++ {
		fmt.Fprintf(&s, "track v%d model_vibraphone {}\n", i)
	}
	s.WriteString("pattern tune notes steps=4 { c4 . e4 . }\nscene main {")
	for i := 0; i < tracks; i++ {
		fmt.Fprintf(&s, " v%d = tune", i)
	}
	s.WriteString(" }\nsong { main }\n")
	return s.String()
}

// acidSource declares tracks of one voice each.
func acidSource(tracks int) string {
	var s strings.Builder
	s.WriteString("cicada 2\ntempo 120\n")
	for i := 0; i < tracks; i++ {
		fmt.Fprintf(&s, "track t%d acid {}\n", i)
	}
	s.WriteString("pattern riff acid { 1 . 5 . }\nscene main {")
	for i := 0; i < tracks; i++ {
		fmt.Fprintf(&s, " t%d = riff", i)
	}
	s.WriteString(" }\nsong { main }\n")
	return s.String()
}

const masterInsertSource = `cicada 2
tempo 120
track bass acid {}
pattern riff acid { 1 . 5 . }
scene main { bass = riff }
song { main }
fx tone eq { type = highpass frequency = 25Hz q = 0.707 }
master { level = 6dB insert = tone }
`

func writeBrowserScore(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// wam2Inputs writes the stand-ins cicada wam2 reads: a WAM SDK folder and a
// kernel. Neither is played, so they only need to exist. The kernel carries the
// keys kernel's name, which wam2 requires for a score with keys-family tracks,
// and a vocals kernel sits beside it for a score that has track inserts.
func wam2Inputs(t *testing.T) (kernel, sdk string) {
	t.Helper()
	dir := t.TempDir()
	sdk = filepath.Join(dir, "sdk")
	for _, name := range []string{"dist/index.js", "src/RingBuffer_LICENSE.txt"} {
		path := filepath.Join(sdk, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	kernel = filepath.Join(dir, "cicada-keys.wasm")
	for _, name := range []string{kernel, filepath.Join(dir, "cicada-vocals.wasm")} {
		if err := os.WriteFile(name, []byte("\x00asm\x01\x00\x00\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return kernel, sdk
}

func exportWAM2(t *testing.T, path string) error {
	t.Helper()
	kernel, sdk := wam2Inputs(t)
	return wam2Command([]string{path, "-o", filepath.Join(t.TempDir(), "plugin"), "--kernel", kernel, "--sdk", sdk}, &bytes.Buffer{})
}

func TestCheckBrowserProfileListsReservedVoices(t *testing.T) {
	path := writeBrowserScore(t, browserBandSource)
	var stdout, stderr bytes.Buffer
	if err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("diagnostics for a score that fits: %s", stderr.String())
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 7 || lines[0] != path || !strings.HasPrefix(lines[1], "  browser profile: 4 of 16 tracks, 15 of 32 voices reserved, image ") || !strings.HasSuffix(lines[1], " of 2097152 bytes") {
		t.Fatalf("report:\n%s", stdout.String())
	}
	want := "    track  kind              voices\n" +
		"    lead   acid                   1\n" +
		"    vibes  model_vibraphone       4\n" +
		"    harp   harp                   4\n" +
		"    drums  drums                  6"
	if got := strings.Join(lines[2:], "\n"); got != want {
		t.Fatalf("per-track voices:\n%s\nwant:\n%s", got, want)
	}

	// Without the chord step the harp takes eight voices, which the report shows.
	single := writeBrowserScore(t, strings.Replace(browserBandSource, "[c4 e4 g4] . . .", "c4 . . .", 1))
	stdout.Reset()
	if err := checkCommand([]string{"--profile=browser", single}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	for _, want := range []string{"19 of 32 voices reserved", "    harp   harp                   8\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, stdout.String())
		}
	}

	// The desktop profile is unchanged: it names the score and nothing else.
	stdout.Reset()
	if err := checkCommand([]string{path}, &stdout, &stderr); err != nil || stdout.String() != path+"\n" {
		t.Fatalf("desktop check: %v %q", err, stdout.String())
	}
}

func TestCheckBrowserProfileCountsKeysVoices(t *testing.T) {
	// A keys-family track reserves its voices control, 1 to 8, and 8 when unset.
	const source = `cicada 2
tempo 120
key c major
track keys tine_ep { voices = 6 }
track pad string_machine { voices = 4 }
track organ tine_ep {}
pattern comp notes steps=4 { [c4 e4 g4] . . . }
scene main { keys = comp pad = comp organ = comp }
song { main }
`
	path := writeBrowserScore(t, source)
	var stdout, stderr bytes.Buffer
	if err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	for _, want := range []string{
		"3 of 16 tracks, 18 of 32 voices reserved",
		"    keys   tine_ep              6\n",
		"    pad    string_machine       4\n",
		"    organ  tine_ep              8\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, stdout.String())
		}
	}
	if err := exportWAM2(t, path); err != nil {
		t.Fatalf("wam2 refused a score that fits: %v", err)
	}
	// Two more tracks that never play still reserve eight voices each: 34 in all.
	over := writeBrowserScore(t, strings.Replace(source, "track organ tine_ep {}\n", "track organ tine_ep {}\ntrack idle1 tine_ep {}\ntrack idle2 tine_ep {}\n", 1))
	stdout.Reset()
	stderr.Reset()
	if err := checkCommand([]string{"--profile", "browser", over}, &stdout, &stderr); err == nil || !strings.Contains(stderr.String(), "5 of 16 tracks, 34 of 32 voices reserved") || !strings.Contains(stderr.String(), "    idle2  tine_ep              8\n") {
		t.Fatalf("overflowing keys tracks: %v\n%s", err, stderr.String())
	}
}

func TestCheckBrowserProfileRefusesWhatWAM2Refuses(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		summary      string // the totals the report gives
		hint         string
	}{
		{"voices", vibraphoneSource(9), "browser profile: 9 of 16 tracks, 36 of 32 voices reserved", "remove tracks or lower voices"},
		{"tracks", acidSource(17), "browser profile: 17 of 16 tracks, 17 of 32 voices reserved", "remove tracks or lower voices"},
		{"master insert", masterInsertSource, "browser profile: 1 of 16 tracks, 1 of 32 voices reserved", "cannot carry a master insert; put a compressor on bus music, or move other effects to track inserts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeBrowserScore(t, tc.source)
			// The desktop profile accepts the score, which is why only wam2 refused it.
			var stdout, stderr bytes.Buffer
			if err := checkPaths([]string{path}, &stdout, &stderr); err != nil {
				t.Fatalf("desktop check: %v\n%s", err, stderr.String())
			}
			wam2Err := exportWAM2(t, path)
			if wam2Err == nil {
				t.Fatal("wam2 exported a score the browser profile must refuse")
			}
			stdout.Reset()
			stderr.Reset()
			err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr)
			if err == nil || err.Error() != "1 error(s) across 1 score(s)" {
				t.Fatalf("browser check: %v\n%s", err, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("a refused score was reported as fitting: %q", stdout.String())
			}
			for _, want := range []string{path + ":1:1: error CICADA-LIMIT: browser profile: " + wam2Err.Error(), tc.hint, tc.summary, "    track  kind"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("diagnostic lacks %q:\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestCheckBrowserProfileListsEveryTrackThatOverflows(t *testing.T) {
	path := writeBrowserScore(t, vibraphoneSource(9))
	var stdout, stderr bytes.Buffer
	if err := checkCommand([]string{path, "--profile", "browser"}, &stdout, &stderr); err == nil {
		t.Fatal("nine vibraphones fit the browser profile")
	}
	for i := 0; i < 9; i++ {
		want := fmt.Sprintf("    v%d     model_vibraphone       4\n", i)
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, stderr.String())
		}
	}
}

func TestCheckBrowserProfileAcceptsTheLimitsExactly(t *testing.T) {
	// Eight vibraphones reserve 32 voices and sixteen acid tracks fill the track limit.
	for _, tc := range []struct{ source, summary string }{
		{vibraphoneSource(8), "8 of 16 tracks, 32 of 32 voices reserved"},
		{acidSource(16), "16 of 16 tracks, 16 of 32 voices reserved"},
	} {
		path := writeBrowserScore(t, tc.source)
		var stdout, stderr bytes.Buffer
		if err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), tc.summary) {
			t.Fatalf("%s: %v\n%s%s", tc.summary, err, stdout.String(), stderr.String())
		}
		if err := exportWAM2(t, path); err != nil {
			t.Fatalf("wam2 refused a score that fits: %v", err)
		}
	}
}

// The hint for a master insert names two moves. Each must pass the browser
// profile and the export; the music bus takes only a compressor, so the hint must
// not send an equalizer there.
func TestCheckBrowserMasterInsertAdviceHolds(t *testing.T) {
	const head = `cicada 2
tempo 120
track bass acid {}
pattern riff acid { 1 . 5 . }
scene main { bass = riff }
song { main }
`
	const equalizer = "fx tone eq { type = highpass frequency = 25Hz q = 0.707 }\n"
	const compressor = "fx squash comp { threshold = -18dB ratio = 3 attack = 5ms release = 120ms makeup = 0dB mix = 1 }\n"
	for _, tc := range []struct {
		name, source, refusal string // refusal is the desktop error, if any
	}{
		{"compressor on bus music", head + compressor + "bus music { insert = squash }\n", ""},
		{"equalizer in a track insert", strings.Replace(head, "track bass acid {}", "track bass acid { insert = tone }", 1) + equalizer, ""},
		{"equalizer on bus music", head + equalizer + "bus music { insert = tone }\n", "music bus insert of eq is not implemented"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeBrowserScore(t, tc.source)
			var stdout, stderr bytes.Buffer
			err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr)
			if tc.refusal != "" {
				if err == nil || !strings.Contains(stderr.String(), tc.refusal) {
					t.Fatalf("want %q: %v\n%s", tc.refusal, err, stderr.String())
				}
				return
			}
			if err != nil || !strings.Contains(stdout.String(), "browser profile: 1 of 16 tracks") {
				t.Fatalf("%v\n%s%s", err, stdout.String(), stderr.String())
			}
			if err := exportWAM2(t, path); err != nil {
				t.Fatalf("wam2 refused a score that fits: %v", err)
			}
		})
	}
}

// A sampler asset sits in the image as float samples. 600,000 mono frames are
// 2.4 MB, over the 2 MiB limit, and the encoder returns no image to measure. The
// report gives the audio as a lower bound of the image, and says what to do.
func TestCheckBrowserProfileReportsAnOversizeImage(t *testing.T) {
	const frames = 600_000
	wav := testwav.Bytes(48000, 1, 16, frames, 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wave.wav"), wav, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.cicada")
	source := fmt.Sprintf(`cicada 2
tempo 120
asset wave "wave.wav" { sha256 = "%x" format = wav frames = %d rate = 48000Hz channels = 1 }
sampler hit { asset = wave root = c3 mode = loop voices = 2 }
track lead acid {}
track long hit {}
pattern riff acid { 1 . 5 . }
pattern tune notes steps=4 { c3 . g3 . }
scene main { lead = riff long = tune }
song { main }
`, sha256.Sum256(wav), frames)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := checkPaths([]string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("desktop check: %v\n%s", err, stderr.String())
	}
	wam2Err := exportWAM2(t, path)
	if wam2Err == nil || !strings.Contains(wam2Err.Error(), "project image exceeds") {
		t.Fatalf("wam2 did not refuse the image for its size: %v", wam2Err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr); err == nil {
		t.Fatalf("an oversize image fits the browser profile:\n%s", stdout.String())
	}
	report := stderr.String()
	for _, want := range []string{
		path + ":1:1: error CICADA-LIMIT: browser profile: " + wam2Err.Error(),
		"shorten or remove audio assets and samples",
		"browser profile: 2 of 16 tracks, 3 of 32 voices reserved, image at least ",
		" of 2097152 bytes",
		"    long   hit ",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	size := regexp.MustCompile(`image at least (\d+) of`).FindStringSubmatch(report)
	if size == nil {
		t.Fatalf("report gives no size:\n%s", report)
	}
	// The samples alone are 4 bytes each; the report must not understate them.
	if bytes, _ := strconv.Atoi(size[1]); bytes < 4*frames || bytes <= 2<<20 {
		t.Errorf("image is at least %d bytes, but the audio holds %d and the limit is %d", bytes, 4*frames, 2<<20)
	}
}

// The image stores every asset sample once and every pack region once, so a
// region that several zones share counts once.
func TestResidentAudioBytesCountsAssetsAndSharedPackRegionsOnce(t *testing.T) {
	shared := make([]float32, 100)
	var cfg engine.Config
	cfg.Tracks = 2
	cfg.Assets = []engine.AudioAsset{{Left: make([]float32, 10), Right: make([]float32, 10)}, {Left: make([]float32, 5)}}
	cfg.Track[1].Prepared = &engine.PackFactory{Zones: []sample.Zone{
		{Region: sample.Region{Left: shared, Right: make([]float32, 50)}},
		{Region: sample.Region{Left: shared}}, // a second zone on the same region
	}}
	// 25 asset samples and 150 pack samples, 4 bytes each.
	if got := residentAudioBytes(&cfg); got != 700 {
		t.Fatalf("resident audio is %d bytes, want 700", got)
	}
}

func TestCheckBrowserProfileKeepsDesktopDiagnostics(t *testing.T) {
	path := writeBrowserScore(t, strings.Replace(browserBandSource, "drums = beat", "drums = missing", 1))
	var stdout, stderr bytes.Buffer
	if err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr); err == nil {
		t.Fatal("a score with a source error passed")
	}
	if strings.Contains(stdout.String(), "browser profile") || !strings.Contains(stderr.String(), path+":") {
		t.Fatalf("source error was not reported first:\n%s%s", stdout.String(), stderr.String())
	}
}

func TestCheckArgsTakeTheProfileAndScore(t *testing.T) {
	path := writeBrowserScore(t, browserBandSource)
	for _, tc := range []struct {
		args    []string
		browser bool
	}{
		{[]string{path}, false},
		{[]string{path, "--profile", "desktop"}, false},
		{[]string{"--profile", "desktop", path}, false},
		{[]string{"--profile", "browser", path}, true},
		{[]string{"--profile=browser", path}, true},
		{[]string{"-profile", "browser", path}, true},
		{[]string{path, "--profile=browser"}, true},
	} {
		var stdout, stderr bytes.Buffer
		if err := checkCommand(tc.args, &stdout, &stderr); err != nil {
			t.Fatalf("%v: %v\n%s", tc.args, err, stderr.String())
		}
		if got := strings.Contains(stdout.String(), "browser profile:"); got != tc.browser {
			t.Errorf("%v: browser report %t, want %t", tc.args, got, tc.browser)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--profile", "mobile", path}, `unknown profile "mobile" (valid: desktop, browser)`},
		{[]string{"--profile"}, "usage: cicada check [--profile desktop|browser] [score.cicada]"},
		{[]string{"--bogus", path}, "usage: cicada check [--profile desktop|browser] [score.cicada]"},
		{[]string{path, path}, "usage: cicada check [--profile desktop|browser] [score.cicada]"},
		{[]string{"--profile", "browser", path, path}, "usage: cicada check [--profile desktop|browser] [score.cicada]"},
		{[]string{filepath.Dir(path)}, "usage: cicada check [--profile desktop|browser] [score.cicada]"},
	} {
		var stdout, stderr bytes.Buffer
		if err := checkCommand(tc.args, &stdout, &stderr); err == nil || err.Error() != tc.want {
			t.Errorf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestCheckBrowserProfileOfAProjectCountsEveryFile(t *testing.T) {
	root := inTempWorkingDirectory(t)
	files := map[string]string{
		"cicada.mod":  "project band\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts.cicada\"\n",
		"main.cicada": "cicada 2\ntempo 120\nscene main { lead = riff vibes = tune harp = strum drums = beat }\nsong { main }\n",
	}
	// The tracks, instrument and patterns live in the second file.
	parts := strings.TrimPrefix(browserBandSource, "cicada 2\ntempo 120\nkey c major\nseed 7\n")
	parts = parts[:strings.Index(parts, "scene main")]
	files["parts.cicada"] = parts
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if err := checkCommand([]string{"--profile", "browser"}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "main.cicada\n  browser profile: 4 of 16 tracks, 15 of 32 voices reserved") {
		t.Fatalf("project report:\n%s", stdout.String())
	}
}
