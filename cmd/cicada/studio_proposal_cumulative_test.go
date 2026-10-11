package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/project"
)

func proposalProject(t *testing.T, source string, edition int) (*studio, http.Handler, string, string) {
	t.Helper()
	path := studioTestPath(t, source)
	manifest := filepath.Join(filepath.Dir(path), "cicada.mod")
	if err := os.WriteFile(manifest, []byte(fmt.Sprintf("project proposals\ncicada %d\n", edition)), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	return s, s.domainRoutes(), path, manifest
}

func assertProposalResponseMatchesDisk(t *testing.T, path string, r *httptest.ResponseRecorder) []byte {
	t.Helper()
	var reply struct{ Source, Revision string }
	disk, err := os.ReadFile(path)
	if err != nil || r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.Source != string(disk) || reply.Revision != studioRevision(disk) {
		t.Fatalf("response does not match disk: %d %s; disk=%q, err=%v", r.Code, r.Body.String(), disk, err)
	}
	return disk
}

func TestStudioProposalAuxiliaryOnlyAcceptUndoRedo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	source := strings.Replace(studioScore, "track bass acid {}", "track bass acid { level = -6dB }", 1)
	s, handler, path, manifest := proposalProject(t, source, 1)
	originalManifest, _ := os.ReadFile(manifest)
	id := stageProposal(t, handler, source, "Upgrade only", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`), ConfirmUpgrade: true}, &edits.ReplaceText{Source: source})
	staged := s.proposals[id].staged.Result
	if !bytes.Equal(staged.Source, []byte(source)) || len(staged.Files) != 1 || staged.Unchanged {
		t.Fatalf("fixture must stage only an auxiliary change: %+v", staged)
	}
	r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
	assertProposalResponseMatchesDisk(t, path, r)
	gotManifest, _ := os.ReadFile(manifest)
	history := studioHistoryEdits(t, handler)
	records, _, err := editlog.ReadLog(editlog.Path(path))
	if !bytes.Equal(gotManifest, staged.Files[0].After) || len(history) != 1 || history[0].Label != "Upgrade only" || err != nil || len(records) != 1 || len(records[0].Intents) != 2 || len(s.proposals) != 0 {
		t.Fatalf("auxiliary commit lost: manifest=%q, history=%+v, records=%+v, err=%v", gotManifest, history, records, err)
	}
	for _, c := range []struct {
		route string
		want  []byte
	}{{"/api/undo", originalManifest}, {"/api/redo", staged.Files[0].After}} {
		r := studioCall(t, handler, c.route, studioEdit{Revision: studioRevision([]byte(source))})
		if got := assertProposalResponseMatchesDisk(t, path, r); string(got) != source {
			t.Fatalf("%s changed the entry source", c.route)
		}
		if got, _ := os.ReadFile(manifest); !bytes.Equal(got, c.want) {
			t.Fatalf("%s manifest=%q, want %q", c.route, got, c.want)
		}
	}
}

func TestStudioProposalResponseMatchesFinalCumulativeSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, handler, path := proposalFixture(t, studioMixerScore)
	final := strings.Replace(studioMixerScore, "level = -6dB", "level = -9dB", 1)
	id := stageProposal(t, handler, studioMixerScore, "Final bass", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)}, &edits.ReplaceText{Source: final})
	r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
	if disk := assertProposalResponseMatchesDisk(t, path, r); string(disk) != final {
		t.Fatal("accepted bytes differ from final candidate")
	}
}

func TestCommitTailCanonicalResponseOverridesMetadata(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			s, _, path := proposalFixture(t, studioMixerScore)
			next := []byte(studioMixerScore)
			if changed {
				next = append(next, []byte("// accepted\n")...)
			}
			outcome := s.commitMutationLocked(studioEdit{Revision: studioRevision([]byte(studioMixerScore))}, []byte(studioMixerScore), studioMutation{Source: next, Response: map[string]any{"source": "superseded", "revision": "superseded", "receipt": "keep"}}, nil, studioHistoryWriteNew, 0, commitHook{})
			disk, _ := os.ReadFile(path)
			if outcome.Status != http.StatusOK || outcome.Response["source"] != string(disk) || outcome.Response["revision"] != studioRevision(disk) || outcome.Response["receipt"] != "keep" || outcome.Written != changed {
				t.Fatalf("canonical response: %+v", outcome)
			}
		})
	}
}

// Compare real sequential commits with one staged proposal. Secondary-file
// publication supplies project inputs to Stage, just as an edit-core host does.
func TestGeneratedProposalsAcceptFinalCumulativeResult(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	pack, err := recording.Build("captured", []recording.Hit{{Rate: 48000, Root: 60, Peak: .5, SourceSHA256: strings.Repeat("a", 64), PCM: []float32{0, .25, .5, .25, 0}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for length := 2; length <= 5; length++ {
		for variant := range 4 {
			for seed := int64(0); seed < 4; seed++ {
				t.Run(fmt.Sprintf("length=%d/variant=%d/seed=%d", length, variant, seed), func(t *testing.T) {
					newline := "\n"
					if seed%2 != 0 {
						newline = "\r\n"
					}
					entry := strings.Replace(studioScore, "track bass acid {}", "track bass acid { level = -6dB }", 1)
					entry = strings.ReplaceAll(entry, "\n", newline)
					edition := 2
					if variant == 1 || variant == 2 {
						edition = 1
					}
					fixture := func() *studio {
						t.Helper()
						root := t.TempDir()
						path := filepath.Join(root, "main.cicada")
						manifest := fmt.Sprintf("project cumulative\ncicada %d\nentry \"main.cicada\"\n", edition)
						files := map[string]string{"main.cicada": entry}
						if variant == 3 {
							manifest += "source \"part.cicada\"\n"
							files["part.cicada"] = "// preserve the secondary scene" + newline + "scene alternate { bass=pulse drums=beat }" + newline
							if err := pack.Write(filepath.Join(root, "assets", "captured")); err != nil {
								t.Fatal(err)
							}
						}
						files["cicada.mod"] = strings.ReplaceAll(manifest, "\n", newline)
						for file, data := range files {
							if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0o600); err != nil {
								t.Fatal(err)
							}
						}
						s, err := newStudio(path)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = s.shutdown() })
						return s
					}
					batch, sequential := fixture(), fixture()
					readState := func(s *studio) map[string]string {
						t.Helper()
						names := []string{"main.cicada", "cicada.mod"}
						if variant == 3 {
							names = append(names, "part.cicada")
						}
						state := make(map[string]string)
						for _, name := range names {
							data, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), name))
							if err != nil {
								t.Fatal(err)
							}
							state[name] = string(data)
						}
						return state
					}
					initial := readState(batch)
					intentsFor := func(s *studio) []edits.Intent {
						rng := rand.New(rand.NewSource(seed + int64(length*100+variant*10)))
						intents := []edits.Intent{&edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`), ConfirmUpgrade: true}}
						for i := 1; i < length; i++ {
							var intent edits.Intent = &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(fmt.Sprintf("%d", -9-rng.Intn(10)))}
							if i == length-1 {
								switch variant {
								case 1:
									intent = &edits.ReplaceText{Source: entry}
								case 3:
									intent = &edits.PublishRecorded{Name: "captured", Declaration: strings.Replace(pack.Source("assets/captured/manifest.json", 60), "voices = 16", "voices = 4", 1), Scene: "alternate", ScenePath: filepath.Join(filepath.Dir(s.path), "part.cicada"), Level: "-6dB", Note: "c4"}
								}
								if variant == 0 || variant == 2 {
									intent = &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-9`)}
								}
							}
							intents = append(intents, intent)
						}
						return intents
					}
					inputs := func(s *studio, opts *edits.Options) error {
						loaded, err := project.ReadSources(s.path, nil)
						if err == nil {
							opts.Sources = loaded.Files
						}
						return err
					}
					for _, intent := range intentsFor(sequential) {
						current, _ := os.ReadFile(sequential.path)
						env := edits.Envelope{Version: 1, Revision: studioRevision(current), Intents: []edits.Intent{intent}}
						r := httptest.NewRecorder()
						sequential.applyPreparedIntents(r, studioEdit{Revision: env.Revision}, env, edits.ParamWriterAuto, func(_ []byte, opts *edits.Options) (edits.Envelope, error) {
							return env, inputs(sequential, opts)
						}, nil, nil)
						assertProposalResponseMatchesDisk(t, sequential.path, r)
					}
					want := readState(sequential)
					proposal := edits.Proposal{ID: "generated", Label: "Cumulative proposal", Envelope: edits.Envelope{Version: 1, Revision: studioRevision([]byte(entry)), Author: "tester", Session: "generated", Intents: intentsFor(batch)}}
					opts, err := batch.editOptions()
					if err != nil {
						t.Fatal(err)
					}
					if err := inputs(batch, &opts); err != nil {
						t.Fatal(err)
					}
					staged, err := edits.Stage([]byte(entry), proposal, opts)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(readState(batch), initial) || string(staged.Result.Source) != want["main.cicada"] {
						t.Fatal("staging wrote files or differs from sequential commits")
					}
					final, err := compileStudioSource(sequential.path, []byte(want["main.cicada"]))
					if err != nil {
						t.Fatal(err)
					}
					wantOps := make([]edits.PreviewOp, 0)
					for _, intent := range proposal.Envelope.Intents {
						if in, ok := intent.(*edits.SetParam); ok {
							resolved, err := project.ResolveParameterPath(final, in.Entity.Name())
							if err != nil {
								t.Fatal(err)
							}
							address, err := project.ParamAddressByName(final, in.Entity.Name())
							if err != nil {
								t.Fatal(err)
							}
							wantOps = append(wantOps, edits.PreviewOp{Kind: "setparam", Track: int(resolved.Track), Param: strings.TrimPrefix(in.Entity.Name(), resolved.Owner+"."), Value: address.Value.(float64)})
						}
					}
					if !reflect.DeepEqual(staged.Ops, wantOps) {
						t.Errorf("preview differs from final compiled values: got %+v, want %+v", staged.Ops, wantOps)
					}
					batch.proposals = map[string]stagedProposal{proposal.ID: {proposal: proposal, staged: staged}}
					batch.previewProposal = proposal.ID
					batch.transport.applyPreview(staged.Ops)
					handler := batch.domainRoutes()
					r := studioCall(t, handler, "/api/proposals/"+proposal.ID+"/accept", map[string]any{})
					assertProposalResponseMatchesDisk(t, batch.path, r)
					if !reflect.DeepEqual(readState(batch), want) {
						t.Fatalf("accepted files differ from sequential commits: got %+v, want %+v", readState(batch), want)
					}
					entries := studioHistoryEdits(t, handler)
					records, _, err := editlog.ReadLog(editlog.Path(batch.path))
					if len(entries) != 1 || entries[0].Label != proposal.Label || err != nil || len(records) != 1 || records[0].Author != "tester" || records[0].Session != "generated" || len(records[0].Intents) != length || len(batch.proposals) != 0 || len(batch.transport.previewOps) != 0 {
						t.Fatalf("acceptance must be one attributed edit: history=%+v, records=%+v, err=%v", entries, records, err)
					}
					for _, c := range []struct {
						route string
						state map[string]string
					}{{"/api/undo", initial}, {"/api/redo", want}} {
						current, _ := os.ReadFile(batch.path)
						r := studioCall(t, handler, c.route, studioEdit{Revision: studioRevision(current)})
						assertProposalResponseMatchesDisk(t, batch.path, r)
						if !reflect.DeepEqual(readState(batch), c.state) {
							t.Fatalf("%s did not restore entry and auxiliary files", c.route)
						}
						if c.route == "/api/undo" && !batch.history.historySnapshot().CanRedo {
							t.Fatal("undo lost redo")
						}
					}
				})
			}
		}
	}
}
