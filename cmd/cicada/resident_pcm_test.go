package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/internal/testpack"
)

func TestParseByteSize(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int64
	}{
		{"1GiB", 1 << 30},
		{"512MiB", 512 << 20},
		{"512mib", 512 << 20},
		{" 64 MiB ", 64 << 20},
		{"1024KiB", 1 << 20},
		{"1TiB", 1 << 40},
		{"1073741824", 1 << 30},
		{"4096B", 4096},
		{"2GB", 2_000_000_000},
		{"500MB", 500_000_000},
		{"1mb", 1_000_000}, // all lower case is a byte unit
		{"1GIB", 1 << 30},  // so is all in capitals
		{"3KB", 3000},
		{"9007199254740991", 9007199254740991},
	} {
		if got, err := parseByteSize(tc.text); err != nil || got != tc.want {
			t.Errorf("parseByteSize(%q) = %d, %v; want %d", tc.text, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ text, want string }{
		{"", "not a whole number"},
		{"MiB", "not a whole number"},
		{"-1GiB", "not a whole number"},
		{"1.5GiB", "not a whole number"},
		{"1,000MiB", "not a whole number"},
		{"1 2 MiB", "not a whole number"},
		{"1XiB", "unknown unit"},
		{"1GiBs", "unknown unit"},
		{"0", "more than zero"},
		{"0MiB", "more than zero"},
		{"99999999999999999999", "too large"},
		{"9223372036854775807KiB", "too large"},
		// A mixed-case spelling can read as bits ("Mb" is megabits); the message offers both byte units.
		{"1Mb", "write MB for powers of 1000 or MiB for powers of 1024"},
		{"2Gb", "write GB for powers of 1000 or GiB for powers of 1024"},
		{"1mB", "can read as bits or as bytes"},
		{"1gIb", "can read as bits or as bytes"},
	} {
		if got, err := parseByteSize(tc.text); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parseByteSize(%q) = %d, %v; want an error containing %q", tc.text, got, err, tc.want)
		}
	}
}

// The message for an unknown unit lists every unit that the parser accepts.
func TestParseByteSizeNamesEveryAcceptedUnitInTheUnknownUnitError(t *testing.T) {
	_, err := parseByteSize("1k")
	if err == nil {
		t.Fatal("the unit k was accepted")
	}
	for _, unit := range byteUnits {
		if !strings.Contains(err.Error(), unit.name) {
			t.Errorf("the error does not offer %s: %v", unit.name, err)
		}
		if _, err := parseByteSize("1" + unit.name); err != nil {
			t.Errorf("the unit %s that the error offers was refused: %v", unit.name, err)
		}
	}
}

func TestMaxResidentPCMFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv(maxResidentPCMEnv, "2GiB")
	if _, limit, err := parseMaxResidentPCM([]string{"song.cicada"}); err != nil || limit != 2<<30 {
		t.Fatalf("without the option the cap must come from %s: %d, %v", maxResidentPCMEnv, limit, err)
	}
	rest, limit, err := parseMaxResidentPCM([]string{"song.cicada", "--max-resident-pcm", "256MiB"})
	if err != nil || limit != 256<<20 || !reflect.DeepEqual(rest, []string{"song.cicada"}) {
		t.Fatalf("the option must win over the environment: %q, %d, %v", rest, limit, err)
	}
	t.Setenv(maxResidentPCMEnv, "lots")
	if _, _, err := parseMaxResidentPCM(nil); err == nil || !strings.Contains(err.Error(), maxResidentPCMEnv) {
		t.Fatalf("a bad value must name the variable: %v", err)
	}
	// The option makes the environment irrelevant, so a bad value does not matter.
	if _, limit, err := parseMaxResidentPCM([]string{"--max-resident-pcm=1GiB"}); err != nil || limit != 1<<30 {
		t.Fatalf("an option given: %d, %v", limit, err)
	}
	t.Setenv(maxResidentPCMEnv, "  ")
	if _, limit, err := parseMaxResidentPCM(nil); err != nil || limit != 0 {
		t.Fatalf("an empty variable must select the default: %d, %v", limit, err)
	}
}

func TestParseMaxResidentPCMTakesTheOptionOutOfTheArguments(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		rest  []string
		limit int64
	}{
		{[]string{"song.cicada"}, []string{"song.cicada"}, 0},
		{[]string{"song.cicada", "--max-resident-pcm", "512MiB", "--audio", "null"}, []string{"song.cicada", "--audio", "null"}, 512 << 20},
		{[]string{"--max-resident-pcm=1GiB", "song.cicada"}, []string{"song.cicada"}, 1 << 30},
		{[]string{"-max-resident-pcm", "2GiB"}, []string{}, 2 << 30},
		{[]string{"-max-resident-pcm=2GiB"}, []string{}, 2 << 30},
		{[]string{"--max-resident-pcm", "1GiB", "--max-resident-pcm=256MiB"}, []string{}, 256 << 20}, // the last wins
	} {
		rest, limit, err := parseMaxResidentPCM(tc.args)
		if err != nil || !reflect.DeepEqual(rest, tc.rest) || limit != tc.limit {
			t.Errorf("parseMaxResidentPCM(%q) = %q, %d, %v; want %q, %d", tc.args, rest, limit, err, tc.rest, tc.limit)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"song.cicada", "--max-resident-pcm"}, "--max-resident-pcm needs a size"},
		{[]string{"--max-resident-pcm", "lots"}, "--max-resident-pcm: size \"lots\" is not a whole number"},
		{[]string{"--max-resident-pcm=0"}, "--max-resident-pcm: size \"0\" must be more than zero"},
	} {
		if _, _, err := parseMaxResidentPCM(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parseMaxResidentPCM(%q) = %v; want an error containing %q", tc.args, err, tc.want)
		}
	}
}

func TestRenderArgsCarryTheResidentPCMCap(t *testing.T) {
	_, options, _ := renderArgs([]string{"-o", "out.wav", "--max-resident-pcm", "512MiB", "--bars", "4"}, 24)
	if options.MaxResidentPCM != 512<<20 || options.Bars != 4 {
		t.Fatalf("render options: %+v", options)
	}
	_, options, _ = renderArgs([]string{"-o", "out.wav"}, 24)
	if options.MaxResidentPCM != 0 {
		t.Fatalf("without the option the cap must stay at the default: %+v", options)
	}
	_, options, _ = renderArgs([]string{"-o", "out", "--max-resident-pcm=1GiB"}, 32)
	if options.MaxResidentPCM != 1<<30 {
		t.Fatalf("stems options: %+v", options)
	}
}

// liveCapScore writes a score with one 400,000-byte pack and returns its path.
func liveCapScore(t *testing.T) (string, testpack.Pack) {
	t.Helper()
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 100_000})
	path := filepath.Join(dir, "main.cicada")
	source := "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\npattern melody notes steps=4 { c4 . . . }\nscene main { lead = melody }\nsong { main*8 }\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, pack
}

func TestPlayCompilesSamplerPacksUnderTheResidentCap(t *testing.T) {
	path, pack := liveCapScore(t)
	if pack.PCMBytes != 400_000 {
		t.Fatalf("fixture holds %d bytes, want 400000", pack.PCMBytes)
	}
	if _, err := compileLiveScoreAtRateCapped(path, liveSampleRate, 0); err != nil {
		t.Fatalf("the default cap refused a 400 KB pack: %v", err)
	}
	if _, err := compileLiveScoreAtRateCapped(path, liveSampleRate, 400_000); err != nil {
		t.Fatalf("a cap equal to the pack refused it: %v", err)
	}
	_, err := compileLiveScoreAtRateCapped(path, liveSampleRate, 399_999)
	if err == nil {
		t.Fatal("a cap a byte below the pack admitted it")
	}
	for _, want := range []string{"--max-resident-pcm", "399999 bytes", "400000 bytes", pack.Manifest} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
}

// A reload while playing is compiled again, so it meets the same cap.
func TestLiveWatchAppliesTheResidentCapToAnEdit(t *testing.T) {
	path, _ := liveCapScore(t)
	const limit = 500_000
	initialHash, err := playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := compileLiveScoreAtRateCapped(path, liveSampleRate, limit)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var log bytes.Buffer
	watcher := liveScoreWatcher{path: path, last: initialHash, stream: stream, output: &log, maxResidentPCM: limit}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A second sampler on another pack of 400,000 bytes makes 800,000 bytes.
	second := testpack.Write(t, filepath.Dir(path), testpack.Options{ID: "second", Frames: 100_000})
	edited := strings.Replace(string(source), "track lead sound {}", second.Sampler("echo", 2)+"track lead sound {}", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher.poll()
	if got := log.String(); !strings.Contains(got, "--max-resident-pcm") || !strings.Contains(got, "continuing previous score") {
		t.Fatalf("an edit over the cap was not refused with the previous score kept: %q", got)
	}
}

// An edit that leaves the packs alone reloads them from memory. The playing
// score, the score waiting for the next bar and the score being compiled would
// otherwise hold three copies of every pack.
func TestLiveWatchReloadReusesThePacksOfThePlayingScore(t *testing.T) {
	// reload plays a score with one pack of the given length, edits a comment
	// and returns the heap that the first compile and the reload allocated.
	reload := func(frames int) (first, second uint64, pcm int64) {
		dir := t.TempDir()
		pack := testpack.Write(t, dir, testpack.Options{Frames: frames})
		path := filepath.Join(dir, "main.cicada")
		source := "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\npattern melody notes steps=4 { c4 . . . }\nscene main { lead = melody }\nsong { main*8 }\n"
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(instrumentpack.ReleaseShared)
		initialHash, err := playSourceHash(path)
		if err != nil {
			t.Fatal(err)
		}
		var initial liveplay.Score
		first = allocatedBytes(func() { initial, err = compileLiveScoreAtRateCapped(path, liveSampleRate, 0) })
		if err != nil {
			t.Fatal(err)
		}
		stream, err := liveplay.New(initial, liveSampleRate)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		var log bytes.Buffer
		watcher := liveScoreWatcher{path: path, last: initialHash, stream: stream, output: &log}
		if err := os.WriteFile(path, []byte(source+"// an edit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		second = allocatedBytes(watcher.poll)
		if !strings.Contains(log.String(), "queued for next bar") {
			t.Fatalf("the edit was not queued: %q", log.String())
		}
		return first, second, pack.PCMBytes
	}
	// The engine allocates for itself whatever the pack is. Measure that on a
	// tiny pack, and look for the 32 MB of PCM above it.
	_, tiny, _ := reload(1000)
	first, second, pcm := reload(8_000_000)
	if first < uint64(pcm) {
		t.Fatalf("fixture: the first compile allocated %d bytes for %d bytes of PCM", first, pcm)
	}
	if second > tiny+uint64(pcm)/2 {
		t.Errorf("the reload allocated %d bytes for a pack of %d bytes of PCM (a tiny pack: %d): it decoded the pack again", second, pcm, tiny)
	}
}

func TestStudioTakesTheResidentPCMCapAsTheProcessDefault(t *testing.T) {
	t.Cleanup(func() { instrumentpack.SetDefaultMaxResidentPCM(0) })
	limit := func() int64 {
		tally, err := instrumentpack.NewResidentCap(0)
		if err != nil {
			t.Fatal(err)
		}
		return tally.Limit()
	}
	rest, err := applyStudioResidentPCM([]string{"score.cicada", "--max-resident-pcm", "3GiB", "--lsp-stdio"})
	if err != nil || !reflect.DeepEqual(rest, []string{"score.cicada", "--lsp-stdio"}) || limit() != 3<<30 {
		t.Fatalf("with the option: %q, %v, cap %d", rest, err, limit())
	}
	t.Setenv(maxResidentPCMEnv, "2GiB")
	if _, err = applyStudioResidentPCM([]string{"score.cicada"}); err != nil || limit() != 2<<30 {
		t.Fatalf("from the environment: %v, cap %d", err, limit())
	}
	t.Setenv(maxResidentPCMEnv, "")
	if _, err = applyStudioResidentPCM([]string{"score.cicada"}); err != nil || limit() != instrumentpack.DefaultMaxResidentPCM {
		t.Fatalf("without either the cap must return to the default: %v, cap %d", err, limit())
	}
	if _, err = applyStudioResidentPCM([]string{"--max-resident-pcm", "lots"}); err == nil || !strings.Contains(err.Error(), "--max-resident-pcm") {
		t.Fatalf("a bad size: %v", err)
	}
}

func TestAuditionTakesTheResidentPCMCap(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{Frames: 4000}) // 16,000 bytes
	path := filepath.Join(dir, "main.cicada")
	source := "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\npattern melody notes steps=4 { c4 . . . }\nscene main { lead = melody }\nsong { main*2 }\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(instrumentpack.ReleaseShared)
	if err := auditionCommand([]string{path, "--json", "--max-resident-pcm", "16000"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("a cap equal to the pack refused it: %v", err)
	}
	err := auditionCommand([]string{path, "--json", "--max-resident-pcm=15999"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "15999 bytes") || !strings.Contains(err.Error(), "--max-resident-pcm") {
		t.Fatalf("a cap a byte below the pack: %v", err)
	}
	t.Setenv(maxResidentPCMEnv, "15999")
	if err = auditionCommand([]string{path, "--json"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "15999 bytes") {
		t.Fatalf("the cap from the environment: %v", err)
	}
	if err = auditionCommand([]string{path, "--max-resident-pcm"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "needs a size") {
		t.Fatalf("a flag with no size: %v", err)
	}
}
