package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
)

var vocalAssetLine = regexp.MustCompile(`asset (\S+) "([^"]+)" \{ sha256 = "([0-9a-f]+)"`)

// vocalNormalize replaces the names a run generates (take ids, printed asset
// names, paths and digests) with stable placeholders so outputs can be pinned.
func vocalNormalize(out []byte, takes ...takejournal.Take) []byte {
	text := string(out)
	for i, take := range takes {
		text = strings.ReplaceAll(text, take.ID, fmt.Sprintf("TAKE-%d", i+1))
	}
	seen := map[string]bool{}
	for _, match := range vocalAssetLine.FindAllStringSubmatch(text, -1) {
		if seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		n := len(seen)
		for _, pair := range [][2]string{{match[3], fmt.Sprintf("SHA-%d", n)}, {match[2], fmt.Sprintf("PATH-%d.wav", n)}, {match[1], fmt.Sprintf("ASSET-%d", n)}} {
			text = strings.ReplaceAll(text, pair[0], pair[1])
		}
	}
	return []byte(text)
}

// vocalRun applies the PlaceVocalComp intent through the host and compares the
// outcome with the one the retired writer produced for the same score and take.
type vocalRun struct {
	golden map[string]m10Result
	calls  map[string]int
}

func (r *vocalRun) check(t *testing.T, s *studio, source []byte, take takejournal.Take, settings *studioRecordingSettings, wantOK bool, takes ...takejournal.Take) []byte {
	t.Helper()
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	got, env, err := s.vocalTakeEnvelope(source, take, settings)
	if (err == nil) != wantOK {
		t.Fatalf("error %v, want success %v", err, wantOK)
	}
	r.calls[t.Name()]++
	m10Check(t, r.golden, fmt.Sprintf("%s#%d", t.Name(), r.calls[t.Name()]), vocalNormalize(got, takes...), err)
	if err != nil {
		return nil
	}
	if len(env.Intents) != 1 || env.Intents[0].Kind() != "placevocalcomp" || env.Revision != studioRevision(source) {
		t.Fatalf("envelope %+v", env)
	}
	return got
}

func TestVocalCompIntentWritesTheRecordedBytes(t *testing.T) {
	run := &vocalRun{golden: m10Golden(t, "vocal"), calls: map[string]int{}}
	scenes := "cicada 2\ntempo 120\ntrack lead acid {}\ntrack vox audio {}\npattern one acid steps=4 { 1 . 5 . }\npattern two acid steps=4 { 3 . 6 . }\nscene a { lead=one vox=off }\nscene hold { lead=keep }\nscene b { lead=two }\nscene quiet { lead=off }\nsong { a hold b quiet a }\n"
	twoBlocks := strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*2 }", 1)
	arranged := "cicada 2\ntempo 120\n// keep\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\narrange {\n  // opening\n  place a bass pulse { at = 0ticks length = 7680ticks }\n}\n"
	s := newTakeStudio(t, t.TempDir())
	punch := func(start, end string) *studioRecordingSettings {
		return &studioRecordingSettings{Start: start, End: end}
	}
	take := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .1)
	second := fakeTimelineTake(t, s, seq.TicksPerBar, 2*seq.TicksPerBar, 192000, .7)
	fade := 5.0
	for _, c := range []struct {
		name     string
		source   string
		settings *studioRecordingSettings
		ok       bool
	}{
		{"scene song full take", twoBlocks, nil, true},
		{"scene song punch", twoBlocks, punch("@1.2.1", "@2.1.1"), true},
		{"scene song with gain and offset", twoBlocks, &studioRecordingSettings{Start: "@1.1.1", End: "@2.1.1", OffsetMS: 12, FadeMS: &fade}, true},
		{"scene bindings and holds", scenes, punch("@1.1.1", "@2.1.1"), true},
		{"existing arrangement", arranged, punch("@1.1.1", "@2.1.1"), true},
		{"out of coverage", twoBlocks, punch("@1.1.1", "@9.1.1"), false},
		{"bad region", twoBlocks, punch("@2.1.1", "@1.1.1"), false},
		{"bad gain", twoBlocks, &studioRecordingSettings{GainDB: ptr(99.0)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			run.check(t, s, []byte(c.source), take, c.settings, c.ok, take)
		})
	}
	t.Run("replacing an earlier print", func(t *testing.T) {
		first := run.check(t, s, []byte(twoBlocks), take, nil, true, take, second)
		run.check(t, s, first, second, punch("@2.1.1", "@3.1.1"), true, take, second)
		// A refined phrase on the same take keeps one placement and one block.
		again := run.check(t, s, first, take, punch("@1.2.1", "@2.1.1"), true, take, second)
		if bytes.Count(again, []byte("cicada-vocal-begin")) != 1 {
			t.Fatalf("managed block duplicated:\n%s", again)
		}
	})
	t.Run("edited managed block", func(t *testing.T) {
		first := run.check(t, s, []byte(twoBlocks), take, nil, true, take, second)
		run.check(t, s, bytes.Replace(first, []byte("// cicada-vocal-begin vox\n"), nil, 1), second, punch("@2.1.1", "@3.1.1"), false, take, second)
		run.check(t, s, bytes.Replace(first, []byte("// cicada-vocal-end vox\n"), nil, 1), second, punch("@2.1.1", "@3.1.1"), false, take, second)
	})
	t.Run("extra declaration inside the block", func(t *testing.T) {
		first := run.check(t, s, []byte(twoBlocks), take, nil, true, take, second)
		edited := bytes.Replace(first, []byte("// cicada-vocal-end vox\n"), []byte("track extra audio {}\n// cicada-vocal-end vox\n"), 1)
		run.check(t, s, edited, second, punch("@2.1.1", "@3.1.1"), true, take, second)
	})
	t.Run("placement name taken", func(t *testing.T) {
		taken := strings.TrimSuffix(arranged, "}\n") + "  place vocal-vox bass pulse { at = 7680ticks length = 3840ticks }\n}\n"
		run.check(t, s, []byte(taken), take, punch("@1.1.1", "@2.1.1"), false, take, second)
	})
}

func ptr[T any](v T) *T { return &v }

// Selecting a Session take records the comp intent; a candidate that arrives
// without its envelope (a recovered one) records none.
func TestVocalCompCommitLogsItsIntent(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*2 }", 1))
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	a := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .1)
	if err := s.commitTake(a.ID, a.Expected, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(s.path)
	b := fakeTimelineTake(t, s, seq.TicksPerBar, 2*seq.TicksPerBar, 192000, .7)
	candidate, err := s.vocalTakeSource(first, b, &studioRecordingSettings{Start: "@2.1.1", End: "@3.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitTake(b.ID, b.Expected, candidate); err != nil {
		t.Fatal(err)
	}
	records, _, err := editlog.ReadLog(editlog.Path(s.path))
	if err != nil || len(records) != 2 {
		t.Fatalf("%+v %v", records, err)
	}
	if len(records[0].Intents) != 1 || !strings.Contains(string(records[0].Intents[0]), `"kind":"placevocalcomp"`) || records[0].Label != "Select audio take" {
		t.Fatalf("first comp: %+v", records[0])
	}
	if len(records[1].Intents) != 0 {
		t.Fatalf("a candidate without its envelope logged %+v", records[1].Intents)
	}
}
