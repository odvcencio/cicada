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
	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/notation"
)

// Compare generated HTTP batches with sequential single-intent HTTP requests,
// including repeated edits to each auxiliary path and exact undo/redo.
func TestGeneratedIntentBatchesMatchSequentialHTTP(t *testing.T) {
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
					sequentialHandler := s.domainRoutes()
					for _, intent := range intents {
						r := studioCall(t, sequentialHandler, "/api/intents", edits.Envelope{Version: 1, Revision: edits.Revision(state[path]), Intents: []edits.Intent{intent}})
						assertProposalResponseMatchesDisk(t, path, r)
						for file := range state {
							var err error
							state[file], err = os.ReadFile(file)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					sequentialPlan, err := (&studioCompiler{path: path}).Compile(state[path], nil)
					if err != nil {
						t.Fatal(err)
					}
					write(initial)
					if err := os.Remove(editlog.Path(path)); err != nil {
						t.Fatal(err)
					}
					s, err = newStudio(path)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = s.shutdown() })
					opts, err := s.editOptions(entry, nil)
					if err != nil {
						t.Fatal(err)
					}
					env := edits.Envelope{Version: edits.EnvelopeVersion, Revision: edits.Revision(entry), Intents: intents}
					batch, err := edits.Apply(entry, env, opts)
					if err != nil {
						t.Fatalf("batch: %v", err)
					}
					if !bytes.Equal(batch.Source, state[path]) || !reflect.DeepEqual(batch.Plan.Scenes, sequentialPlan.Scenes) || !reflect.DeepEqual(batch.Plan.Tracks, sequentialPlan.Tracks) || !reflect.DeepEqual(batch.Plan.Patterns, sequentialPlan.Patterns) {
						t.Fatal("batch bytes or compiled music differ from sequential HTTP")
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
					handler := s.domainRoutes()
					before := noopFiles(t, root)
					env.DryRun = true
					r := studioCall(t, handler, "/api/intents", env)
					if r.Code != 200 || !strings.Contains(r.Body.String(), "part0.cicada") || !reflect.DeepEqual(before, noopFiles(t, root)) || len(studioHistoryEdits(t, handler)) != 0 {
						t.Fatalf("auxiliary dry run: %d %s", r.Code, r.Body.String())
					}
					env.DryRun = false
					r = studioCall(t, handler, "/api/intents", env)
					assertProposalResponseMatchesDisk(t, path, r)
					records, skipped, err := editlog.ReadLog(editlog.Path(path))
					if err != nil || skipped != 0 || len(records) != 1 || len(records[0].Intents) != len(intents) || len(studioHistoryEdits(t, handler)) != 1 {
						t.Fatalf("batch log: %+v, skipped=%d, err=%v", records, skipped, err)
					}
					for file, want := range state {
						got, err := os.ReadFile(file)
						if err != nil || !bytes.Equal(got, want) {
							t.Fatalf("committed %s differs from sequential result: %v", filepath.Base(file), err)
						}
					}
					for _, c := range []struct {
						route string
						want  map[string][]byte
					}{{"/api/undo", initial}, {"/api/redo", state}} {
						current, _ := os.ReadFile(path)
						r := studioCall(t, handler, c.route, studioEdit{Revision: edits.Revision(current)})
						if r.Code != http.StatusOK {
							t.Fatalf("%s: %d %s", c.route, r.Code, r.Body.String())
						}
						for file, want := range c.want {
							if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, want) {
								t.Fatalf("%s did not restore %s: %v", c.route, filepath.Base(file), err)
							}
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
			opts, err := s.editOptions(entry, nil)
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
