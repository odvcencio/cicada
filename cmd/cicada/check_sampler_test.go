package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/internal/testpack"
)

// samplerScore plays the pack through one track and one scene.
func samplerScore(pack testpack.Pack, patterns string) string {
	return "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\n" + patterns + "scene main { lead = melody }\nsong { main }\n"
}

// writeSamplerProject writes the pack and a score that plays it. The pack covers
// MIDI 48 to 72 (c3 to c5) at every velocity unless options say otherwise.
func writeSamplerProject(t *testing.T, options testpack.Options, patterns string) (path, source string, pack testpack.Pack) {
	t.Helper()
	dir := t.TempDir()
	pack = testpack.Write(t, dir, options)
	source = samplerScore(pack, patterns)
	path = filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, source, pack
}

// positionOf is the one-based line and column of the first text in source at or
// after the first occurrence of from.
func positionOf(t *testing.T, source, from, text string) (line, column int) {
	t.Helper()
	start := strings.Index(source, from)
	if start < 0 {
		t.Fatalf("%q is not in the source", from)
	}
	at := strings.Index(source[start:], text)
	if at < 0 {
		t.Fatalf("%q does not follow %q in the source", text, from)
	}
	at += start
	line = 1 + strings.Count(source[:at], "\n")
	column = 1 + utf8.RuneCountInString(source[strings.LastIndex(source[:at], "\n")+1:at])
	return line, column
}

func TestCheckReportsEachNoteOutsideEveryZoneAtItsSourcePosition(t *testing.T) {
	path, source, pack := writeSamplerProject(t, testpack.Options{}, "pattern melody notes { c1 . c4 . c6 . . . }\n")
	var stdout, stderr bytes.Buffer
	err := checkPaths([]string{path}, &stdout, &stderr)
	if err == nil || err.Error() != "2 error(s) across 1 score(s)" {
		t.Fatalf("a score with two notes outside the pack's zones: %v\n%s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("a refused score was reported as passing: %q", stdout.String())
	}
	report := stderr.String()
	for _, note := range []struct {
		written string
		midi    int
	}{{"c1", 24}, {"c6", 84}} {
		line, column := positionOf(t, source, "pattern melody", note.written)
		want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-ZONE: pitch %s (MIDI %d) on track lead, pattern melody, is outside every zone of the pack for sampler sound (%s, which covers MIDI 48-72)",
			path, line, column, note.written, note.midi, pack.Manifest)
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
		sourceLine := strings.Split(source, "\n")[line-1]
		if caret := "  " + sourceLine + "\n  " + strings.Repeat(" ", column-1) + "^\n"; !strings.Contains(report, caret) {
			t.Errorf("report lacks the source line with its caret at column %d:\n%s", column, report)
		}
	}
	if strings.Contains(report, "pitch c4") {
		t.Errorf("a note inside the zone was reported:\n%s", report)
	}
	// The sampler's root moves only a pitch written without an octave, so the
	// advice says that and puts the octave and the transpose first.
	if !strings.Contains(report, "write it in another octave, change the pattern's transpose") || !strings.Contains(report, "the sampler's root sets the octave of a pitch written without one") {
		t.Errorf("report gives no suggestion:\n%s", report)
	}
	if first, second := strings.Index(report, "pitch c1"), strings.Index(report, "pitch c6"); first < 0 || second < first {
		t.Errorf("notes are not reported in source order:\n%s", report)
	}
}

func TestCheckPassesNotesInsideTheZonesToTheirEdges(t *testing.T) {
	path, _, _ := writeSamplerProject(t, testpack.Options{}, "pattern melody notes { c3 . c4 . c5 . . . }\n")
	var stdout, stderr bytes.Buffer
	if err := checkPaths([]string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("notes at MIDI 48, 60 and 72 are inside the zone: %v\n%s", err, stderr.String())
	}
	if stdout.String() != path+"\n" || stderr.Len() != 0 {
		t.Fatalf("report: %q, %q", stdout.String(), stderr.String())
	}
}

func TestCheckReportsAVelocityNoZoneCovers(t *testing.T) {
	quiet := testpack.Options{Zones: []testpack.Zone{{KeyLow: 48, KeyHigh: 72, VelocityHigh: 60}}}
	path, source, _ := writeSamplerProject(t, quiet, "pattern melody notes { c4 . . . }\n")
	var stdout, stderr bytes.Buffer
	// A step plays at velocity 100 unless the pattern says otherwise.
	if err := checkPaths([]string{path}, &stdout, &stderr); err == nil {
		t.Fatalf("velocity 100 is above every zone:\n%s", stdout.String())
	}
	line, column := positionOf(t, source, "pattern melody", "c4")
	if want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-ZONE: pitch c4 (MIDI 60) at velocity 100 on track lead", path, line, column); !strings.Contains(stderr.String(), want) {
		t.Fatalf("report lacks %q:\n%s", want, stderr.String())
	}
}

func TestCheckAppliesThePatternTranspose(t *testing.T) {
	up := "pattern melody notes steps=4 transpose=24 { c4 . . . }\n"
	path, source, _ := writeSamplerProject(t, testpack.Options{}, up)
	var stdout, stderr bytes.Buffer
	if err := checkPaths([]string{path}, &stdout, &stderr); err == nil {
		t.Fatalf("c4 transposed up two octaves is MIDI 84, above the zone:\n%s", stdout.String())
	}
	line, column := positionOf(t, source, "pattern melody", "c4")
	if want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-ZONE: pitch c4 (MIDI 84) on track lead, pattern melody", path, line, column); !strings.Contains(stderr.String(), want) {
		t.Fatalf("report lacks %q:\n%s", want, stderr.String())
	}
	// The same written note passes when the transpose brings it back into the zone.
	down := "pattern melody notes steps=4 transpose=-12 { c6 . . . }\n"
	path, _, _ = writeSamplerProject(t, testpack.Options{}, down)
	stdout.Reset()
	stderr.Reset()
	if err := checkPaths([]string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("c6 transposed down an octave is MIDI 72, inside the zone: %v\n%s", err, stderr.String())
	}
}

func TestCheckIgnoresPatternsNoSceneBindsToThePackTrack(t *testing.T) {
	path, _, _ := writeSamplerProject(t, testpack.Options{}, "pattern melody notes { c4 . . . }\npattern spare notes { c6 . . . }\n")
	var stdout, stderr bytes.Buffer
	if err := checkPaths([]string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("a pattern that no scene plays cannot fault the engine: %v\n%s", err, stderr.String())
	}
}

func TestCheckReportsEverySamplerOnAPackThatTheTracksShare(t *testing.T) {
	dir := t.TempDir()
	pack := testpack.Write(t, dir, testpack.Options{})
	source := "cicada 2\ntempo 120\n" + pack.Sampler("first", 2) + pack.Sampler("second", 2) +
		"track one first {}\ntrack two second {}\npattern melody notes { c6 . . . }\nscene main { one = melody two = melody }\nsong { main }\n"
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := checkPaths([]string{path}, &stdout, &stderr); err == nil || err.Error() != "2 error(s) across 1 score(s)" {
		t.Fatalf("one note, two tracks: %v\n%s", err, stderr.String())
	}
	for _, track := range []string{"first", "second"} {
		if want := "pitch c6 (MIDI 84) on track " + map[string]string{"first": "one", "second": "two"}[track] + ", pattern melody, is outside every zone of the pack for sampler " + track; !strings.Contains(stderr.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, stderr.String())
		}
	}
}

func TestCheckReportsAPackThatDoesNotLoad(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, dir string, pack testpack.Pack)
		want   string
	}{
		{"missing manifest", func(t *testing.T, dir string, pack testpack.Pack) {
			if err := os.Remove(filepath.Join(dir, pack.Manifest)); err != nil {
				t.Fatal(err)
			}
		}, "manifest.json"},
		{"manifest that differs from its pin", func(t *testing.T, dir string, pack testpack.Pack) {
			file, err := os.OpenFile(filepath.Join(dir, pack.Manifest), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err = file.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
		}, "instrument manifest hash mismatch"},
		{"missing audio", func(t *testing.T, dir string, pack testpack.Pack) {
			if err := os.Remove(filepath.Join(dir, filepath.Dir(pack.Manifest), "samples", "take0.wav.gz")); err != nil {
				t.Fatal(err)
			}
		}, "take0.wav.gz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, source, pack := writeSamplerProject(t, testpack.Options{}, "pattern melody notes { c4 . . . }\n")
			tc.damage(t, filepath.Dir(path), pack)
			var stdout, stderr bytes.Buffer
			err := checkPaths([]string{path}, &stdout, &stderr)
			if err == nil || err.Error() != "1 error(s) across 1 score(s)" {
				t.Fatalf("a pack that does not load passed: %v\n%s", err, stderr.String())
			}
			line, column := positionOf(t, source, "sampler sound", "sampler")
			want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-PACK: sampler sound: ", path, line, column)
			if !strings.Contains(stderr.String(), want) || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("report lacks %q and %q:\n%s", want, tc.want, stderr.String())
			}
		})
	}
}

func TestCheckBrowserProfileAlsoReportsNotesOutsideEveryZone(t *testing.T) {
	path, source, _ := writeSamplerProject(t, testpack.Options{}, "pattern melody notes { c4 . c6 . }\n")
	var stdout, stderr bytes.Buffer
	err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr)
	if err == nil || err.Error() != "1 error(s) across 1 score(s)" {
		t.Fatalf("browser profile: %v\n%s", err, stderr.String())
	}
	line, column := positionOf(t, source, "pattern melody", "c6")
	if want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-ZONE: pitch c6 (MIDI 84)", path, line, column); !strings.Contains(stderr.String(), want) {
		t.Fatalf("report lacks %q:\n%s", want, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("a refused score was reported as fitting: %q", stdout.String())
	}
	// The same pack passes when the notes are inside its zones, and the report lists the track.
	path, _, _ = writeSamplerProject(t, testpack.Options{}, "pattern melody notes { c4 . c5 . }\n")
	stdout.Reset()
	stderr.Reset()
	if err = checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "browser profile: 1 of 16 tracks") || !strings.Contains(stdout.String(), "    lead   sound ") {
		t.Fatalf("browser profile of a pack in the zones: %v\n%s%s", err, stdout.String(), stderr.String())
	}
}

// The command exits with status 1 for a note outside every zone and 0 for notes
// inside them. The child process runs the real main.
func TestCheckExitStatusForNotesOutsideEveryZone(t *testing.T) {
	if score := os.Getenv("CICADA_CHECK_CHILD_SCORE"); score != "" {
		os.Args = []string{"cicada", "check", score}
		main()
		return
	}
	for _, tc := range []struct {
		name, patterns string
		status         int
	}{
		{"outside", "pattern melody notes { c4 . c6 . }\n", 1},
		{"inside", "pattern melody notes { c4 . c5 . }\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _, _ := writeSamplerProject(t, testpack.Options{}, tc.patterns)
			child := exec.Command(os.Args[0], "-test.run=^TestCheckExitStatusForNotesOutsideEveryZone$")
			child.Env = append(os.Environ(), "CICADA_CHECK_CHILD_SCORE="+path)
			output, err := child.CombinedOutput()
			status := 0
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				status = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if status != tc.status {
				t.Fatalf("exit status %d, want %d:\n%s", status, tc.status, output)
			}
			if mentioned := strings.Contains(string(output), "CICADA-SAMPLER-ZONE"); mentioned != (tc.status == 1) {
				t.Fatalf("zone diagnostic reported %t:\n%s", mentioned, output)
			}
		})
	}
}

// declareFrames rewrites a pack's manifest to declare frames of PCM in each
// asset, leaving the audio files as they are, and returns the pack with its new
// pin. It makes a pack that opens and reports a size without the audio to match,
// so a test can ask about sizes it could not afford to decode.
func declareFrames(t *testing.T, dir string, pack testpack.Pack, frames, channels int) testpack.Pack {
	t.Helper()
	path := filepath.Join(dir, pack.Manifest)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest instrumentpack.Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Assets {
		manifest.Assets[i].Frames, manifest.Assets[i].Channels = frames, channels
	}
	if data, err = json.Marshal(manifest); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	pack.SHA256, pack.PCMBytes = hex.EncodeToString(sum[:]), int64(len(manifest.Assets))*int64(frames)*int64(channels)*4
	return pack
}

// peakHeap runs f and returns the most heap that was in use at any moment it
// sampled, less the heap in use when it started.
func peakHeap(f func()) uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	base := sample[0].Value.Uint64()
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for {
			metrics.Read(sample)
			if v := sample[0].Value.Uint64(); v > peak.Load() {
				peak.Store(v)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	f()
	close(stop)
	<-done
	return max(peak.Load(), base) - base
}

// The check proves that each pack loads, but it keeps only the zone table.
// Holding every pack until the end would make its memory grow with the number
// of packs a score names.
func TestCheckHoldsOnePackAtATime(t *testing.T) {
	const packs, frames = 6, 4_000_000 // six packs of 16 MB of PCM
	dir := t.TempDir()
	var source strings.Builder
	source.WriteString("cicada 2\ntempo 120\n")
	for i := range packs {
		pack := testpack.Write(t, dir, testpack.Options{ID: fmt.Sprintf("p%d", i), Frames: frames})
		source.WriteString(pack.Sampler(fmt.Sprintf("s%d", i), 2))
		fmt.Fprintf(&source, "track t%d s%d {}\n", i, i)
	}
	source.WriteString("pattern hit notes steps=4 { c4 . . . }\nscene main {")
	for i := range packs {
		fmt.Fprintf(&source, " t%d = hit", i)
	}
	source.WriteString(" }\nsong { main }\n")
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	instrumentpack.ReleaseShared()
	runtime.GC()
	var err error
	peak := peakHeap(func() { err = checkPaths([]string{path}, &bytes.Buffer{}, &bytes.Buffer{}) })
	if err != nil {
		t.Fatal(err)
	}
	// One pack is 16 MB of PCM, and decoding it holds up to as much again in
	// buffers. Six packs held together would be 96 MB before any buffer.
	t.Logf("check peaked at %d MB of heap for %d packs of %d MB", peak>>20, packs, frames*4>>20)
	if limit := uint64(4 * frames * 4); peak > limit {
		t.Fatalf("check peaked at %d MB of heap for %d packs of %d MB; one pack at a time stays under %d MB",
			peak>>20, packs, frames*4>>20, limit>>20)
	}
}

// Render counts the PCM that the manifests declare against the cap before it
// reads any audio. Check reports the refusal that render would give, from the
// same figures, so that a score which check passes is not refused for size.
func TestCheckReportsWhatRenderWouldRefuseForSize(t *testing.T) {
	t.Run("default cap, from manifests alone", func(t *testing.T) {
		// Seventeen packs that declare 64,000,000 bytes each pass 1 GiB at the
		// last one. Their audio is not that long: the check never needs it to know.
		dir := t.TempDir()
		var source strings.Builder
		source.WriteString("cicada 2\ntempo 120\n")
		for i := range 17 {
			pack := declareFrames(t, dir, testpack.Write(t, dir, testpack.Options{ID: fmt.Sprintf("p%02d", i), Frames: 100}), 8_000_000, 2)
			source.WriteString(pack.Sampler(fmt.Sprintf("s%02d", i), 2))
		}
		source.WriteString("track t s00 {}\npattern hit notes steps=4 { c4 . . . }\nscene main { t = hit }\nsong { main }\n")
		path := filepath.Join(dir, "main.cicada")
		if err := os.WriteFile(path, []byte(source.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if err := checkPaths([]string{path}, &stdout, &stderr); err == nil {
			t.Fatal("a score whose packs declare 1.01 GiB passed")
		}
		report := stderr.String()
		if got := strings.Count(report, "CICADA-SAMPLER-MEMORY"); got != 1 {
			t.Fatalf("%d memory diagnostics, want 1, at the pack that crosses the cap:\n%s", got, report)
		}
		line, column := positionOf(t, source.String(), "sampler s16", "sampler")
		want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-MEMORY: sampler packs exceed the native resident PCM cap (--max-resident-pcm): loading packs/p16/manifest.json would hold 1.01 GiB (1088000000 bytes) of decoded PCM, cap 1 GiB (1073741824 bytes); render, stems and play refuse this score",
			path, line, column)
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
		if !strings.Contains(report, "--max-resident-pcm or CICADA_MAX_RESIDENT_PCM") {
			t.Errorf("report does not say how to raise the cap:\n%s", report)
		}
	})

	t.Run("a cap that check is given", func(t *testing.T) {
		dir := t.TempDir()
		first := testpack.Write(t, dir, testpack.Options{ID: "first", Frames: 1000}) // 4000 bytes each
		second := testpack.Write(t, dir, testpack.Options{ID: "second", Frames: 1000})
		source := "cicada 2\ntempo 120\n" + first.Sampler("a", 2) + second.Sampler("b", 2) + "track t a {}\npattern hit notes steps=4 { c4 . . . }\nscene main { t = hit }\nsong { main }\n"
		path := filepath.Join(dir, "main.cicada")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			args    []string
			refused bool
		}{
			{[]string{path}, false},
			{[]string{"--max-resident-pcm", "8000", path}, false},
			{[]string{"--max-resident-pcm=7999", path}, true},
			{[]string{path, "--max-resident-pcm", "6000"}, true},
		} {
			var stdout, stderr bytes.Buffer
			err := checkCommand(tc.args, &stdout, &stderr)
			if tc.refused != (err != nil) || tc.refused != strings.Contains(stderr.String(), "CICADA-SAMPLER-MEMORY") {
				t.Errorf("%q: %v\n%s", tc.args, err, stderr.String())
			}
			if tc.refused {
				line, column := positionOf(t, source, "sampler b", "sampler")
				if want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-MEMORY", path, line, column); !strings.Contains(stderr.String(), want) {
					t.Errorf("%q: the refusal belongs at the declaration of the sampler whose pack crosses the cap:\n%s", tc.args, stderr.String())
				}
			}
		}
		t.Setenv(maxResidentPCMEnv, "6000")
		if err := checkCommand([]string{path}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Error("the cap from the environment was ignored")
		}
	})

	t.Run("zones are still checked", func(t *testing.T) {
		path, _, _ := writeSamplerProject(t, testpack.Options{}, "pattern melody notes { c1 . c4 . }\n")
		var stdout, stderr bytes.Buffer
		err := checkCommand([]string{"--max-resident-pcm", "1000", path}, &stdout, &stderr)
		if err == nil || err.Error() != "2 error(s) across 1 score(s)" || !strings.Contains(stderr.String(), "CICADA-SAMPLER-MEMORY") || !strings.Contains(stderr.String(), "CICADA-SAMPLER-ZONE") {
			t.Fatalf("a score over the cap and with a stray note: %v\n%s", err, stderr.String())
		}
	})
}

// Whether a pack loads depends on the output rate: the playback ratio is the
// recording's rate over the output rate. The browser exports at 44.1, 48 and 96
// kHz, so check admits a pack at each.
func TestCheckAdmitsAPackAtEveryExportedRate(t *testing.T) {
	// Keys 34 to 86 around a root of 60 play at 48 kHz (lowest ratio 0.22) but
	// not at 96 kHz (0.11, below the voice's 0.125).
	wide := testpack.Options{Zones: []testpack.Zone{{KeyLow: 34, KeyHigh: 86}}}
	path, source, _ := writeSamplerProject(t, wide, "pattern melody notes { c4 . . . }\n")
	var stdout, stderr bytes.Buffer
	err := checkPaths([]string{path}, &stdout, &stderr)
	if err == nil || err.Error() != "1 error(s) across 1 score(s)" {
		t.Fatalf("a pack that cannot play at 96 kHz passed: %v\n%s", err, stderr.String())
	}
	line, column := positionOf(t, source, "sampler sound", "sampler")
	want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-PACK: sampler sound: at 96000 Hz: sample playback ratio must be between 0.125 and 8", path, line, column)
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("report lacks %q:\n%s", want, stderr.String())
	}
	// The same pack with narrower keys plays at all three rates.
	path, _, _ = writeSamplerProject(t, testpack.Options{Zones: []testpack.Zone{{KeyLow: 48, KeyHigh: 72}}}, "pattern melody notes { c4 . . . }\n")
	stderr.Reset()
	if err = checkPaths([]string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("a pack that plays at every rate: %v\n%s", err, stderr.String())
	}
}

// A phrase use can move a note out of the pack's keys. The use is where the
// score says so, and the written pitch alone would not explain the key.
func TestCheckReportsANoteOfAPhraseAtTheUse(t *testing.T) {
	patterns := "phrase motif { c4 . e4 . }\npattern melody notes { use motif +13 }\n"
	path, source, _ := writeSamplerProject(t, testpack.Options{}, patterns)
	var stdout, stderr bytes.Buffer
	err := checkPaths([]string{path}, &stdout, &stderr)
	if err == nil || err.Error() != "2 error(s) across 1 score(s)" {
		t.Fatalf("c4 and e4 moved up 13 semitones are MIDI 73 and 77, above the zone: %v\n%s", err, stderr.String())
	}
	line, column := positionOf(t, source, "pattern melody", "use")
	for _, want := range []string{
		fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-ZONE: pitch c4 (MIDI 73) on track lead, pattern melody, from phrase motif used with transpose +13, is outside every zone", path, line, column),
		fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-ZONE: pitch e4 (MIDI 77) on track lead, pattern melody, from phrase motif used with transpose +13, is outside every zone", path, line, column),
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, stderr.String())
		}
	}
	// A phrase used as written is reported at the use too, without a transpose.
	path, source, _ = writeSamplerProject(t, testpack.Options{}, "phrase high { c6 . . . }\npattern melody notes { c4 . . . use high }\n")
	stderr.Reset()
	if err = checkPaths([]string{path}, &stdout, &stderr); err == nil {
		t.Fatal("c6 is above the zone")
	}
	line, column = positionOf(t, source, "pattern melody", "use high")
	if want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-ZONE: pitch c6 (MIDI 84) on track lead, pattern melody, from phrase high, is outside every zone", path, line, column); !strings.Contains(stderr.String(), want) {
		t.Errorf("report lacks %q:\n%s", want, stderr.String())
	}
}
