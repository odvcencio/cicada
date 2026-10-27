package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
	if !strings.Contains(report, "change the sampler's root or the pattern's transpose") {
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
