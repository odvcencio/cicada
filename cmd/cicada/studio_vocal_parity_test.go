package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
)

// vocalParity runs the retired source writer and the PlaceVocalComp intent on
// the same score and take. Both print the same audio, so the files agree.
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

func vocalParity(t *testing.T, s *studio, source []byte, take takejournal.Take, settings *studioRecordingSettings, wantOK bool, recorder *m10Recorder, takes ...takejournal.Take) []byte {
	t.Helper()
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	want, wantErr := s.vocalTakeSource(source, take, settings)
	if wantErr == nil {
		recorder.addNext(t.Name(), vocalNormalize(want, takes...), nil)
	} else {
		recorder.addNext(t.Name(), nil, wantErr)
	}
	got, env, gotErr := s.vocalTakeEnvelope(source, take, settings)
	if (wantErr == nil) != (gotErr == nil) || wantErr != nil && wantErr.Error() != gotErr.Error() {
		t.Fatalf("error %v, want %v", gotErr, wantErr)
	}
	if (wantErr == nil) != wantOK {
		t.Fatalf("writer error %v, want success %v", wantErr, wantOK)
	}
	if wantErr != nil {
		return nil
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes differ\n got: %q\nwant: %q", got, want)
	}
	if len(env.Intents) != 1 || env.Intents[0].Kind() != "placevocalcomp" || env.Revision != studioRevision(source) {
		t.Fatalf("envelope %+v", env)
	}
	return got
}

func TestVocalCompIntentMatchesTheOldWriter(t *testing.T) {
	recorder := newM10Recorder(t, "vocal")
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
			vocalParity(t, s, []byte(c.source), take, c.settings, c.ok, recorder, take)
		})
	}
	t.Run("replacing an earlier print", func(t *testing.T) {
		first := vocalParity(t, s, []byte(twoBlocks), take, nil, true, recorder, take, second)
		vocalParity(t, s, first, second, punch("@2.1.1", "@3.1.1"), true, recorder, take, second)
		// A refined phrase on the same take keeps one placement and one block.
		again := vocalParity(t, s, first, take, punch("@1.2.1", "@2.1.1"), true, recorder, take, second)
		if bytes.Count(again, []byte("cicada-vocal-begin")) != 1 {
			t.Fatalf("managed block duplicated:\n%s", again)
		}
	})
	t.Run("edited managed block", func(t *testing.T) {
		first := vocalParity(t, s, []byte(twoBlocks), take, nil, true, recorder, take, second)
		vocalParity(t, s, bytes.Replace(first, []byte("// cicada-vocal-begin vox\n"), nil, 1), second, punch("@2.1.1", "@3.1.1"), false, recorder, take, second)
		vocalParity(t, s, bytes.Replace(first, []byte("// cicada-vocal-end vox\n"), nil, 1), second, punch("@2.1.1", "@3.1.1"), false, recorder, take, second)
	})
	t.Run("extra declaration inside the block", func(t *testing.T) {
		first := vocalParity(t, s, []byte(twoBlocks), take, nil, true, recorder, take, second)
		edited := bytes.Replace(first, []byte("// cicada-vocal-end vox\n"), []byte("track extra audio {}\n// cicada-vocal-end vox\n"), 1)
		vocalParity(t, s, edited, second, punch("@2.1.1", "@3.1.1"), true, recorder, take, second)
	})
	t.Run("placement name taken", func(t *testing.T) {
		taken := strings.TrimSuffix(arranged, "}\n") + "  place vocal-vox bass pulse { at = 7680ticks length = 3840ticks }\n}\n"
		vocalParity(t, s, []byte(taken), take, punch("@1.1.1", "@2.1.1"), false, recorder, take, second)
	})
}

func ptr[T any](v T) *T { return &v }
