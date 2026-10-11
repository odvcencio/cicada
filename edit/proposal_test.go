package edit_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestStageReturnsValidatedSourceDiffAndResolvedPreview(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(map[string]string{"\n": "lf", "\r\n": "crlf"}[newline], func(t *testing.T) {
			source := []byte(strings.ReplaceAll("cicada 2\n// keep spelling\ntrack bass acid { level = -6dB }\npattern p acid { c3 . }\nscene main { bass=p }\nsong { main }\n", "\n", newline))
			before := bytes.Clone(source)
			proposal := edits.Proposal{ID: "p1", Label: "Bass variation", Envelope: edits.Envelope{Version: 1, Revision: edits.Revision(source), Author: "tester", Session: "s1", Intents: []edits.Intent{&edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`"-3dB"`)}, &edits.SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`0.25`)}}}}
			staged, err := edits.Stage(source, proposal, edits.Options{Compiler: m5Compiler{}})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(source, before) || staged.Result.Label != proposal.Label || staged.Result.Unchanged || staged.Result.Plan.Revision != edits.Revision(staged.Result.Source) {
				t.Fatalf("bad staged result: %+v", staged.Result)
			}
			want := []edits.PreviewOp{{Kind: "setparam", Track: 0, Param: "level", Value: -3}, {Kind: "setparam", Track: 0, Param: "pan", Value: .25}}
			if !reflect.DeepEqual(staged.Ops, want) || !strings.Contains(staged.Diff, "-track bass acid { level = -6dB }") || !strings.Contains(staged.Diff, "+track bass acid { level = -3dB") {
				t.Fatalf("ops=%+v, diff=%q", staged.Ops, staged.Diff)
			}
		})
	}
}

func TestStageResolvesPreviewAgainstCandidatePlan(t *testing.T) {
	source := []byte("cicada 2\ntrack bass acid { level = -6dB }\npattern p acid { c3 . }\nscene main { bass=p }\nsong { main }\n")
	proposal := edits.Proposal{Envelope: edits.Envelope{Version: 1, Revision: edits.Revision(source), Intents: []edits.Intent{&edits.ReplaceText{Source: strings.Replace(string(source), "track bass", "track lead acid {}\ntrack bass", 1)}, &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)}}}}
	staged, err := edits.Stage(source, proposal, edits.Options{Compiler: m5Compiler{}})
	if err != nil || len(staged.Ops) != 1 || staged.Ops[0].Track != 1 {
		t.Fatalf("candidate preview: %+v, %v", staged, err)
	}
}

func TestStageRefusesStaleAndInvalidProposalsAndKeepsNoops(t *testing.T) {
	source := []byte("cicada 2\n" + recordScore)
	opts := edits.Options{Compiler: m5Compiler{}}
	proposal := edits.Proposal{Envelope: edits.Envelope{Version: 1, Revision: "stale", Intents: []edits.Intent{&edits.ReplaceText{Source: string(source)}}}}
	staged, err := edits.Stage(source, proposal, opts)
	var conflict *edits.ConflictError
	if staged != nil || !errors.As(err, &conflict) {
		t.Fatalf("stale proposal: %+v, %v", staged, err)
	}
	proposal.Envelope.Revision = edits.Revision(source)
	staged, err = edits.Stage(source, proposal, opts)
	if err != nil || !staged.Result.Unchanged || staged.Diff != "" || len(staged.Ops) != 0 {
		t.Fatalf("unchanged proposal: %+v, %v", staged, err)
	}
	proposal.Envelope.Intents = []edits.Intent{&edits.ReplaceText{Source: "???"}}
	if staged, err := edits.Stage(source, proposal, opts); staged != nil || err == nil {
		t.Fatalf("invalid proposal: %+v, %v", staged, err)
	}
}

func TestStageDiffIncludesAuxiliaryFilesWithoutHostPaths(t *testing.T) {
	declaration := "sampler captured { asset=wave root=c4 mode=oneshot voices=4 }"
	source := []byte("cicada 2\ntrack bass acid {}\npattern p acid { c3 . }\nasset wave \"recorded.wav\" { sha256=\"" + strings.Repeat("a", 64) + "\" format=wav frames=480 rate=48000Hz channels=1 }\n")
	part := []byte("scene main { bass=p }\nsong { main }\n")
	compiler := multiFileCompiler{path: "main.cicada", files: []notation.SourceFile{{Path: "main.cicada", Source: source}, {Path: "parts/scene.cicada", Source: part}}}
	proposal := edits.Proposal{Envelope: edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.PublishRecorded{Name: "captured", Declaration: declaration, Scene: "main", ScenePath: "parts/scene.cicada", Level: "-3dB", Note: "c4"}}}}
	staged, err := edits.Stage(source, proposal, edits.Options{Path: compiler.path, Sources: compiler.files, Compiler: compiler})
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.Result.Files) != 1 || !strings.Contains(staged.Diff, "--- a/scene.cicada") || !strings.Contains(staged.Diff, "captured_track = captured_taps") || strings.Contains(staged.Diff, "parts/") || !bytes.Equal(part, compiler.files[1].Source) {
		t.Fatalf("auxiliary diff: %q, files=%+v", staged.Diff, staged.Result.Files)
	}
}
