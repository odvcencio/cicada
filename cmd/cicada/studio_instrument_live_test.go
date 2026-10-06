package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
)

const authoredLiveScore = `cicada 1
instrument mono-voice {
  voice mono {
    let amp = adsr(gate, 2ms, 20ms, 0.7, 20ms)
    out = sine(pitch) * amp * velocity * 0.1
  }
}
instrument poly-voice {
  voice poly {
    let amp = adsr(gate, 2ms, 20ms, 0.7, 20ms)
    out = sine(pitch) * amp * velocity * 0.08
  }
}
track mono mono-voice {}
track keys poly-voice {}
pattern mono-notes notes steps=4 { . . . . }
pattern keys-notes notes steps=4 { . . . . }
scene main { mono=mono-notes keys=keys-notes }
song { main }
`

func TestAuthoredInstrumentsCompileToPlayableLiveTargets(t *testing.T) {
	initial, err := compileLiveScore(studioTestPath(t, authoredLiveScore))
	if err != nil {
		t.Fatal(err)
	}
	if initial.Tracks[0].Kind != "graph" || initial.Tracks[1].Kind != "poly" {
		t.Fatalf("authored live routing: %+v", initial.Tracks)
	}
	p, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, track := range []string{"mono", "keys"} {
		if err := p.Note(track, 60, 100, true); err != nil {
			t.Fatal(err)
		}
		if err := p.Note(track, 64, 110, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.CopyN(io.Discard, p, 4096); err != nil {
		t.Fatal(err)
	}
	for _, track := range []string{"mono", "keys"} {
		if err := p.Note(track, 60, 0, false); err != nil {
			t.Fatal(err)
		}
		if err := p.Note(track, 64, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.CopyN(io.Discard, p, 4096); err != nil {
		t.Fatal(err)
	}
}

func TestAuthoredInstrumentsRecordSingleNoteTakes(t *testing.T) {
	for _, item := range [][2]string{{"mono", "mono-notes"}, {"keys", "keys-notes"}} {
		updated, err := recordedTakeSource([]byte(authoredLiveScore), item[0], item[1], []studioTakeNote{{Tick: 0, EndTick: 100, Note: 60, Velocity: 90}, {Tick: 120, EndTick: 220, Note: 64, Velocity: 100}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(updated, []byte("pattern "+item[1]+" notes steps=4 { c4 e4 . . }")) {
			t.Fatalf("instrument take lost notes: %s", updated)
		}
	}
	if _, err := recordedTakeSource([]byte(authoredLiveScore), "keys", "keys-notes", []studioTakeNote{{Tick: 0, EndTick: 180, Note: 60, Velocity: 90}, {Tick: 0, EndTick: 180, Note: 64, Velocity: 100}}); err == nil || !strings.Contains(err.Error(), "one pitch per step") {
		t.Fatalf("polyphonic chord take must be retained without a lossy commit: %v", err)
	}
}

func TestAuthoredNoteTakeKeepsInheritedAudioEditionSource(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "main.cicada")
		source := strings.TrimPrefix(authoredLiveScore, "cicada 1\n")
		source = strings.Replace(source, "track keys poly-voice {}", "track keys poly-voice {}\ntrack input audio {} // retained audio track", 1)
		source = strings.Replace(source, "scene main {", "scene main { input=off", 1)
		before := []byte(strings.ReplaceAll(source, "\n", newline))
		for name, data := range map[string][]byte{"main.cicada": before, "cicada.mod": []byte("project recorded\ncicada 2\n")} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		handler, err := studioHandler(path)
		if err != nil {
			t.Fatal(err)
		}
		edit := studioEdit{Revision: studioRevision(before), Recordings: []studioTakeRecording{{Track: "keys", Pattern: "keys-notes", Notes: []studioTakeNote{{Tick: 0, EndTick: 100, Note: 60, Velocity: 100}, {Tick: 120, EndTick: 220, Note: 64, Velocity: 100}}}}}
		response := studioCall(t, handler, "/api/record", edit)
		if response.Code != 200 {
			t.Fatalf("inherited audio edition note take: %d %s", response.Code, response.Body.String())
		}
		updated, _ := os.ReadFile(path)
		if bytes.Contains(updated, []byte("cicada 2")) || !bytes.Contains(updated, []byte("// retained audio track"+newline)) || !bytes.Contains(updated, []byte("pattern keys-notes notes steps=4 { c4 e4 . . }"+newline)) {
			t.Fatalf("take changed inherited source or lost notes: %s", updated)
		}
		if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(updated, []byte(newline), nil), []byte("\n")) {
			t.Fatal("take changed CRLF conventions")
		}
		if undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(updated)}); undo.Code != 200 {
			t.Fatalf("inherited take Undo: %d", undo.Code)
		}
		restored, _ := os.ReadFile(path)
		if !bytes.Equal(restored, before) {
			t.Fatal("take Undo did not restore the exact headerless audio source")
		}
	}
}
