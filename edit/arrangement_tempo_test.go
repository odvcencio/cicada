package edit_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func tempoEditFixture(newline string) ([]byte, []byte, edits.Options) {
	entry := []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern pulse { 1 . 5 . }\n")
	part := []byte(strings.ReplaceAll("// retain section notes\narrange {\n  place run bass pulse { at = @1.1.1 length = 8bars } // retain placement\n  marker chorus { at = @5.1.1 }\n}\n", "\n", newline))
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "song.cicada", Source: part}}
	c := multiFileCompiler{path: "main.cicada", files: files}
	return entry, part, edits.Options{Path: c.path, Edition: 2, Sources: files, Compiler: c, ParseProject: c.parse}
}

func TestArrangementTempoEditsRespectOwnerAndOrdering(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		entry, part, opts := tempoEditFixture(newline)
		result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{
			&edits.SetArrangementTempo{At: "@5.1.1", Value: "96"},
			&edits.SetArrangementTempo{At: "@1.1.1", Value: "137.3334"},
			&edits.SetArrangementTempo{At: "@3.1.1", Value: "120"},
			&edits.SetArrangementTempo{OldAt: "@1.1.1", At: "@7.1.1", Value: "140"},
			&edits.SetArrangementTempo{OldAt: "@5.1.1", At: "@2.1.1", Value: "98.125"},
			&edits.RemoveArrangementTempo{At: "@3.1.1"},
		}}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(entry, result.Source) || len(result.Files) != 1 || result.Files[0].Path != "song.cicada" || !bytes.Equal(result.Files[0].Before, part) {
			t.Fatal("tempo transaction changed entry or original snapshots")
		}
		after := result.Files[0].After
		for _, line := range strings.Split(string(part), newline) {
			if !strings.Contains(string(after), line) {
				t.Fatal("unrelated source changed", line)
			}
		}
		if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(after, []byte(newline), nil), []byte("\n")) {
			t.Fatal("tempo edits changed line endings")
		}
		want := []project.TempoChange{{Bar: 1, BPMMilli: 98125}, {Bar: 6, BPMMilli: 140000}}
		if !reflect.DeepEqual(result.Plan.TempoChanges, want) {
			t.Fatalf("compiled clock %v, want %v", result.Plan.TempoChanges, want)
		}
		if bytes.Index(after, []byte("tempo @2.1.1")) > bytes.Index(after, []byte("tempo @7.1.1")) {
			t.Fatal("moved steps were not ordered")
		}
	}
}

func TestArrangementTempoInvalidBatchNeverPublishes(t *testing.T) {
	entry, part, opts := tempoEditFixture("\n")
	for _, bad := range []edits.Intent{
		&edits.SetArrangementTempo{At: "@1.2.1", Value: "96"},
		&edits.SetArrangementTempo{At: "@9.1.1", Value: "96"},
		&edits.SetArrangementTempo{At: "@1.1.1", Value: "96"},
		&edits.SetArrangementTempo{OldAt: "@2.1.1", At: "@3.1.1", Value: "96"},
		&edits.SetArrangementTempo{OldAt: "@1.1.1", At: "@5.1.1", Value: "96"},
		&edits.SetArrangementTempo{At: "@3.1.1", Value: "19.9999"},
		&edits.SetArrangementTempo{At: "@3.1.1", Value: "300.01"},
		&edits.SetArrangementTempo{At: "@3.1.1", Value: "NaN"},
		&edits.SetArrangementTempo{At: "@3.1.1", Value: "Inf"},
		&edits.SetArrangementTempo{At: "@3.1.1", Value: "96\nmarker injected { at=@2.1.1 }"},
		&edits.RemoveArrangementTempo{At: "@2.1.1"},
		&edits.RemoveArrangementTempo{},
	} {
		result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{
			&edits.SetArrangementTempo{At: "@1.1.1", Value: "120"},
			&edits.SetArrangementTempo{At: "@5.1.1", Value: "137.333"}, bad,
		}}, opts)
		if err == nil || result != nil || !bytes.Equal(opts.Sources[1].Source, part) {
			t.Fatalf("invalid %v yielded a candidate or changed the source: %v", bad, err)
		}
	}
	blocked := errors.New("recording open")
	opts.CaptureGuard = func() error { return blocked }
	result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SetArrangementTempo{At: "@1.1.1", Value: "96"}}}, opts)
	if result != nil || !errors.Is(err, blocked) {
		t.Fatalf("capture guard ignored: %v", err)
	}
	// An imported arrangement is never writable without its supplied snapshot.
	opts.CaptureGuard, opts.Sources = nil, opts.Sources[:1]
	result, err = edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SetArrangementTempo{At: "@1.1.1", Value: "96"}}}, opts)
	if result != nil || err == nil {
		t.Fatal("unowned arrangement was written")
	}
}
