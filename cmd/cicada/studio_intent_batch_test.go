package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/notation"
)

// Compare a batch with separate Apply calls over each preceding candidate,
// then pass that batch to the real Studio writer. No batch HTTP route is needed.
func TestGeneratedIntentBatchesMatchSequentialApplyAndCommit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	pack, err := recording.Build("captured", []recording.Hit{{Rate: 48000, Root: 60, Peak: .5, SourceSHA256: strings.Repeat("a", 64), PCM: []float32{0, .25, .5, .25, 0}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for length := 2; length <= 5; length++ {
		for auxiliaries := 1; auxiliaries <= 2; auxiliaries++ {
			for seed := int64(0); seed < 4; seed++ {
				t.Run(fmt.Sprintf("length=%d/files=%d/seed=%d", length, auxiliaries, seed), func(t *testing.T) {
					newline := "\n"
					if seed%2 != 0 {
						newline = "\r\n"
					}
					entry := []byte(strings.ReplaceAll("cicada 2\n"+strings.ReplaceAll(studioScore, "main", "home"), "\n", newline))
					path := studioTestPath(t, string(entry))
					root := filepath.Dir(path)
					if err := pack.Write(filepath.Join(root, "assets", "captured")); err != nil {
						t.Fatal(err)
					}
					initial := map[string][]byte{path: entry}
					manifest := "project batch\ncicada 2\nentry \"" + filepath.Base(path) + "\"\n"
					var sources []notation.SourceFile
					for i := range auxiliaries {
						partPath := filepath.Join(root, fmt.Sprintf("part%d.cicada", i))
						initial[partPath] = []byte(fmt.Sprintf("// preserve part %d%s scene scene%d { bass=pulse drums=beat }%s", i, newline, i, newline))
						sources = append(sources, notation.SourceFile{Path: partPath, Source: initial[partPath]})
						manifest += fmt.Sprintf("source \"part%d.cicada\"\n", i)
					}
					initial[filepath.Join(root, "cicada.mod")] = []byte(strings.ReplaceAll(manifest, "\n", newline))
					write := func(files map[string][]byte) {
						t.Helper()
						for file, data := range files {
							if err := os.WriteFile(file, data, 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
					write(initial)
					rng := rand.New(rand.NewSource(seed + int64(length*100)))
					publication := func(part int) edits.Intent {
						return &edits.PublishRecorded{Name: "captured", Declaration: strings.Replace(pack.Source("assets/captured/manifest.json", 60), "voices = 16", "voices = 4", 1), Scene: fmt.Sprintf("scene%d", part), ScenePath: sources[part].Path, Level: "-6dB", Note: "c4"}
					}
					intents := []edits.Intent{publication(0), publication(auxiliaries - 1)}
					for i := 2; i < length; i++ {
						switch rng.Intn(4) {
						case 0:
							intents = append(intents, publication(rng.Intn(auxiliaries)))
						case 1:
							intents = append(intents, &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(fmt.Sprintf("%d", -rng.Intn(9)))})
						case 2:
							intents = append(intents, &edits.ToggleStep{Entity: "step:pulse/0"})
						case 3:
							intents = append(intents, &edits.SetProjectSettings{Title: fmt.Sprintf("Batch %d", rng.Intn(99)), TempoMilli: 130000, Root: "a", Scale: "minor"})
						}
					}
					rng.Shuffle(len(intents), func(i, j int) { intents[i], intents[j] = intents[j], intents[i] })
					s := &studio{path: path, sessionID: "batch-test"}
					state := make(map[string][]byte, len(initial))
					for file, data := range initial {
						state[file] = bytes.Clone(data)
					}
					var sequential *edits.Result
					for _, intent := range intents {
						opts, err := s.editOptions()
						if err != nil {
							t.Fatal(err)
						}
						for _, source := range sources {
							opts.Sources = append(opts.Sources, notation.SourceFile{Path: source.Path, Source: state[source.Path]})
						}
						sequential, err = edits.Apply(state[path], edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}, opts)
						if err != nil {
							t.Fatalf("sequential %s: %v", intent.Kind(), err)
						}
						state[path] = sequential.Source
						for _, file := range sequential.Files {
							if !bytes.Equal(file.Before, state[file.Path]) {
								t.Fatalf("sequential %s has stale Before", intent.Kind())
							}
							state[file.Path] = file.After
						}
						write(state)
					}
					write(initial)
					opts, err := s.editOptions()
					if err != nil {
						t.Fatal(err)
					}
					opts.Sources = sources
					env := edits.Envelope{Version: edits.EnvelopeVersion, Revision: edits.Revision(entry), Intents: intents}
					batch, err := edits.Apply(entry, env, opts)
					if err != nil {
						t.Fatalf("batch: %v", err)
					}
					if !bytes.Equal(batch.Source, state[path]) || !reflect.DeepEqual(batch.Plan.Scenes, sequential.Plan.Scenes) || !reflect.DeepEqual(batch.Plan.Tracks, sequential.Plan.Tracks) || !reflect.DeepEqual(batch.Plan.Patterns, sequential.Plan.Patterns) {
						t.Fatal("batch bytes or compiled music differ from sequential Apply")
					}
					seen := map[string]bool{}
					for _, file := range batch.Files {
						if seen[file.Path] || !bytes.Equal(file.Before, initial[file.Path]) || !bytes.Equal(file.After, state[file.Path]) {
							t.Fatalf("duplicate or noncumulative auxiliary patch: %s", filepath.Base(file.Path))
						}
						seen[file.Path] = true
					}
					if len(seen) != auxiliaries {
						t.Fatalf("got %d auxiliary paths, want %d", len(seen), auxiliaries)
					}
					files, err := auxiliaryFiles(batch.Files)
					if err != nil {
						t.Fatal(err)
					}
					outcome := s.commitMutationLocked(studioEdit{Revision: env.Revision}, entry, studioMutation{Source: batch.Source, Files: files}, nil, studioHistoryWriteNew, 0, commitHook{envelope: &env, compiled: opts.Compiler.(*studioCompiler).compiled(batch.Source, batch.Files)})
					if outcome.Status != http.StatusOK || !outcome.Written {
						t.Fatalf("Studio rejected cumulative candidate: %+v", outcome)
					}
					for file, want := range state {
						got, err := os.ReadFile(file)
						if err != nil || !bytes.Equal(got, want) {
							t.Fatalf("committed %s differs from sequential result: %v", filepath.Base(file), err)
						}
					}
				})
			}
		}
	}
}

func TestIntentBatchUsesStagedManifestEdition(t *testing.T) {
	for _, first := range []string{"setparam", "addeffect"} {
		t.Run(first, func(t *testing.T) {
			entry := []byte(studioScore)
			path := studioTestPath(t, string(entry))
			manifestPath := filepath.Join(filepath.Dir(path), "cicada.mod")
			manifest := []byte("project batch\ncicada 1\n")
			if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			s := &studio{path: path}
			opts, err := s.editOptions()
			if err != nil {
				t.Fatal(err)
			}
			var intent edits.Intent = &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`), ConfirmUpgrade: true}
			if first == "addeffect" {
				intent = &edits.AddEffect{Name: "grit", EffectKind: "drive", ConfirmUpgrade: true}
			}
			result, err := edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent, &edits.SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`0.25`)}, &edits.SetProjectSettings{Title: "Upgraded", TempoMilli: 130000, Root: "a", Scale: "minor"}}}, opts)
			if err != nil {
				t.Fatalf("later intents must see the confirmed edition upgrade: %v", err)
			}
			if len(result.Files) != 1 || result.Files[0].Path != manifestPath || !bytes.Equal(result.Files[0].Before, manifest) || string(result.Files[0].After) != "project batch\ncicada 2\n" || result.Plan.Edition != 2 {
				t.Fatalf("manifest and compiler must see one cumulative edition upgrade: %+v", result.Files)
			}
			if got, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(got, manifest) {
				t.Fatal("Apply wrote the manifest")
			}
			files, err := auxiliaryFiles(result.Files)
			if err != nil {
				t.Fatal(err)
			}
			outcome := s.commitMutationLocked(studioEdit{Revision: edits.Revision(entry)}, entry, studioMutation{Source: result.Source, Files: files}, nil, studioHistoryWriteNew, 0, commitHook{compiled: opts.Compiler.(*studioCompiler).compiled(result.Source, result.Files)})
			if outcome.Status != http.StatusOK || !outcome.Written {
				t.Fatalf("Studio rejected staged manifest upgrade: %+v", outcome)
			}
			if got, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(got, result.Files[0].After) {
				t.Fatalf("Studio did not commit the staged manifest: %v", err)
			}
		})
	}
}
