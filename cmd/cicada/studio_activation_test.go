package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
)

func TestProposalAcceptanceActivatesWithOneHistoryEntry(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		t.Run(fmt.Sprintf("unchanged-%v", unchanged), func(t *testing.T) {
			source := "cicada 2\n" + studioScore
			s, stream := activationStudio(t, source)
			s.history = newStudioHistory([]byte(source))
			s.transport.history = s.history
			handler := s.domainRoutes()
			want := float32(-3)
			intents := []edits.Intent{
				&edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)},
				&edits.SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`0.25`)},
			}
			if unchanged {
				want = -6
				intents = []edits.Intent{&edits.ReplaceText{Source: source}}
			}
			if _, err := stream.SetTrackPreview("bass", kernel.ParamMixGain, want); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.SetTrackPreview("drums", kernel.ParamMixGain, -3); err != nil {
				t.Fatal(err)
			}
			before := noopFiles(t, filepath.Dir(s.path))
			id := stageProposal(t, handler, source, "Live bass proposal", intents...)
			if !unchanged && (len(s.transport.previewOps) != 2 || s.transport.previewOps[0].Track != 0 || s.transport.previewOps[0].Value != -3) {
				t.Fatalf("proposal preview stub lost candidate operations: %+v", s.transport.previewOps)
			}
			if stream.LandedRevision() != 0 || len(studioHistoryEdits(t, handler)) != 0 {
				t.Fatal("staging landed an edit or wrote history")
			}
			response := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
			var reply map[string]any
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reply) != nil {
				t.Fatalf("accept: %d %s", response.Code, response.Body.String())
			}
			saved, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			if reply["source"] != string(saved) || reply["revision"] != studioRevision(saved) || reply["playingRevision"] != studioRevision(saved) {
				t.Fatalf("accept lost canonical source/revision: %+v", reply)
			}
			if len(s.proposals) != 0 || len(s.transport.previewOps) != 0 {
				t.Fatal("acceptance retained the proposal or its stub preview")
			}
			if stream.LandedRevision() != 0 {
				t.Fatal("acceptance landed a patch on the control thread")
			}
			renderStudioPreview(t, stream, 256)
			s.transport.poll()
			if stream.LandedRevision() != 1 || s.transport.snapshot().Pending {
				t.Fatal("accepted proposal did not activate without a replacement offer")
			}
			if value, ok := stream.CommittedValue(0, kernel.ParamMixGain); !ok || value != want {
				t.Fatalf("accepted value %g/%v, want %g", value, ok, want)
			}
			if _, active := stream.OverrideValue(0, kernel.ParamMixGain); active {
				t.Fatal("acceptance did not retire the matching live preview")
			}
			if value, active := stream.OverrideValue(1, kernel.ParamMixGain); !active || value != -3 {
				t.Fatal("acceptance retired an unrelated live gesture")
			}
			history := studioHistoryEdits(t, handler)
			if unchanged {
				if reply["unchanged"] != true || string(saved) != source || len(history) != 0 {
					t.Fatalf("no-op acceptance changed source/history: %+v %+v", reply, history)
				}
				if after := noopFiles(t, filepath.Dir(s.path)); !reflect.DeepEqual(before, after) {
					t.Fatal("no-op acceptance wrote files")
				}
				return
			}
			records, skipped, err := editlog.ReadLog(editlog.Path(s.path))
			if err != nil || skipped != 0 || len(history) != 1 || history[0].Label != "Live bass proposal" || len(records) != 1 || records[0].Label != "Live bass proposal" || records[0].Author != "tester" || records[0].Session != "proposal-session" || len(records[0].Intents) != 2 {
				t.Fatalf("accepted history/log: %+v %+v, skipped %d, error %v", history, records, skipped, err)
			}
			response = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(saved)})
			if response.Code != http.StatusOK {
				t.Fatal(response.Body.String())
			}
			renderStudioPreview(t, stream, 256)
			if restored, err := os.ReadFile(s.path); err != nil || string(restored) != source {
				t.Fatal("one undo did not restore the proposal source")
			}
			if value, ok := stream.CommittedValue(0, kernel.ParamMixGain); !ok || value != -6 || stream.LandedRevision() != 2 {
				t.Fatalf("undo did not activate original gain: %g/%v, revision %d", value, ok, stream.LandedRevision())
			}
		})
	}
}

func activationStudio(t *testing.T, source string) (*studio, *liveplay.Player) {
	s, stream, _ := activationStudioEngine(t, source)
	return s, stream
}

func activationStudioEngine(t *testing.T, source string) (*studio, *liveplay.Player, *engine.Engine) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := compileStudioSource(path, []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildLivePlan(path, []byte(source), liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	plan.prepareScore(&initial)
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	fingerprint, err := playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.stream, transport.playing, transport.last, transport.sampleRate = stream, true, fingerprint, liveSampleRate
	transport.livePlan = plan
	transport.arena = liveplay.NewPatchArena(16)
	return &studio{path: path, lastGoodSource: []byte(source), lastGoodProject: p, transport: transport}, stream, initial.Engine
}

func TestActivationSavedPatchSurvivesSongSeek(t *testing.T) {
	source := "cicada 2\ntrack drums drums { bd_decay=80ms }\npattern beat drums steps=4 { bd: x... }\nscene first { drums=beat drums.bd_level=-6dB }\nscene later { drums=beat }\nsong { first later }\n"
	s, stream, created := activationStudioEngine(t, source)
	renderStudioPreview(t, stream, 256)
	response := httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func(source []byte) ([]byte, error) {
		return bytes.Replace(source, []byte("bd_decay=80ms"), []byte("bd_decay=120ms"), 1), nil
	})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	renderStudioPreview(t, stream, 256)
	if stream.LandedRevision() != 1 {
		t.Fatal("save did not patch")
	}
	for _, index := range []int{1, 0, 1} {
		name := []string{"first", "later"}[index]
		if err := stream.StartSongEntry(index, name); err != nil {
			t.Fatal(err)
		}
		renderStudioPreview(t, stream, 256)
		validator := created.PreviewParamValidator(0)
		actual, _ := validator.CommittedValue(kernel.ParamDrumBdDecay)
		if actual != 120 {
			t.Fatalf("seek %s engine decay %g, want 120", name, actual)
		}
		value, _ := stream.CommittedValue(0, kernel.ParamDrumBdDecay)
		version, err := stream.SetTrackPreview("drums", kernel.ParamDrumBdDecay, 200)
		if err != nil {
			t.Fatal(err)
		}
		renderStudioPreview(t, stream, 256)
		stream.CancelPreviewWait(version)
		renderStudioPreview(t, stream, 256)
		if value != 120 {
			t.Fatalf("seek %s committed decay %g, want 120", name, value)
		}
	}
}

func TestActivationBackwardSeekReconstructsCancellation(t *testing.T) {
	source := "cicada 2\ntrack drums drums { bd_decay=80ms }\npattern beat drums steps=4 { bd: x... }\nscene first { drums=beat }\nscene later { drums=beat drums.bd_decay=90ms }\nscene last { drums=beat }\nsong { first later last }\n"
	_, stream, created := activationStudioEngine(t, source)
	for _, index := range []int{1, 2, 0, 2, 0} {
		name := []string{"first", "later", "last"}[index]
		if err := stream.StartSongEntry(index, name); err != nil {
			t.Fatal(err)
		}
		renderStudioPreview(t, stream, 256)
		want := float64(90)
		if index == 0 {
			want = 80
		}
		validator := created.PreviewParamValidator(0)
		actual, _ := validator.CommittedValue(kernel.ParamDrumBdDecay)
		value, _ := stream.CommittedValue(0, kernel.ParamDrumBdDecay)
		if actual != want || float64(value) != want {
			t.Fatalf("seek %s: engine %g, committed %g, want %g", name, actual, value, want)
		}
		version, err := stream.SetTrackPreview("drums", kernel.ParamDrumBdDecay, 200)
		if err != nil {
			t.Fatal(err)
		}
		renderStudioPreview(t, stream, 256)
		stream.CancelPreviewWait(version)
		renderStudioPreview(t, stream, 256)
		validator = created.PreviewParamValidator(0)
		actual, _ = validator.CommittedValue(kernel.ParamDrumBdDecay)
		if actual != want {
			t.Fatalf("cancel after %s: engine %g, want %g", name, actual, want)
		}
	}
}

func TestActivationNamedKitSceneSettingsBecomeCommittedRestoreValues(t *testing.T) {
	source := "cicada 2\nkit remapped { bd=builtin.sd }\ntrack drums remapped {}\npattern beat drums steps=4 { bd: x... }\nscene main { drums=beat drums.bd_decay=90ms }\nsong { main }\n"
	s, stream := activationStudio(t, source)
	renderStudioPreview(t, stream, 256)
	if value, ok := stream.CommittedValue(0, kernel.ParamDrumBdDecay); !ok || value != 90 {
		t.Fatalf("initial scene committed %g/%v", value, ok)
	}
	updated := strings.Replace(source, "90ms", "120ms", 1)
	response := httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func([]byte) ([]byte, error) { return []byte(updated), nil })
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	s.transport.poll()
	if !s.transport.snapshot().Pending {
		t.Fatal("scene settings did not take the structural path")
	}
	if err := stream.StartSongEntry(0, "main"); err != nil {
		t.Fatal(err)
	}
	renderStudioPreview(t, stream, 256)
	for {
		select {
		case event := <-stream.Events():
			s.transport.markLanded(event)
		default:
			goto landed
		}
	}
landed:
	if value, ok := stream.CommittedValue(0, kernel.ParamDrumBdDecay); !ok || value != 120 {
		t.Fatalf("activated scene committed %g/%v", value, ok)
	}
	version, err := stream.SetTrackPreview("drums", kernel.ParamDrumBdDecay, 200)
	if err != nil {
		t.Fatal(err)
	}
	renderStudioPreview(t, stream, 256)
	stream.CancelPreviewWait(version)
	renderStudioPreview(t, stream, 256)
	if _, active := stream.OverrideValue(0, kernel.ParamDrumBdDecay); active {
		t.Fatal("cancel did not retire scene preview")
	}
	if value, ok := stream.CommittedValue(0, kernel.ParamDrumBdDecay); !ok || value != 120 {
		t.Fatal("cancellation lost the activated scene value")
	}
	response = httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func(source []byte) ([]byte, error) {
		return bytes.Replace(source, []byte("track drums remapped {}"), []byte("track drums remapped { level=-3dB }"), 1), nil
	})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	renderStudioPreview(t, stream, 256)
	if stream.LandedRevision() == 0 {
		t.Fatal("mixer edit after structural activation did not patch")
	}
}

func TestActivationNoopCommitRetiresOnlyMatchingPreview(t *testing.T) {
	s, stream := activationStudio(t, "cicada 2\n"+studioScore)
	if _, err := stream.SetTrackPreview("bass", kernel.ParamMixGain, -6); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.SetTrackPreview("drums", kernel.ParamMixGain, -3); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func(source []byte) ([]byte, error) { return source, nil })
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	renderStudioPreview(t, stream, 256)
	if _, active := stream.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("matching no-op commit did not retire preview")
	}
	if value, active := stream.OverrideValue(1, kernel.ParamMixGain); !active || value != -3 {
		t.Fatal("unrelated gesture retired")
	}
}

func TestCommitPatchesPlayingEngineAndSkipsOffer(t *testing.T) {
	s, stream := activationStudio(t, "cicada 2\n"+studioScore)
	if _, err := stream.SetTrackPreview("bass", kernel.ParamMixGain, -3); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"revision":%q,"path":"bass.level","value":-3}`, studioRevision(s.lastGoodSource))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/mixer", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	s.editMixer(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("mixer edit: %d %s", response.Code, response.Body.String())
	}
	if stream.LandedRevision() != 0 {
		t.Fatal("control thread landed the patch")
	}
	if _, err := io.CopyN(io.Discard, stream, 256*8); err != nil {
		t.Fatal(err)
	}
	if stream.LandedRevision() == 0 {
		t.Fatal("patch did not land on the next block")
	}
	s.transport.poll()
	if state := s.transport.snapshot(); state.Pending {
		t.Fatal("poll offered a full engine after an in-place patch")
	}
	if _, active := stream.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("matching preview was not retired")
	}
	if value, ok := stream.CommittedValue(0, kernel.ParamMixGain); !ok || value != -3 {
		t.Fatalf("saved mixer %g/%v", value, ok)
	}
}

func TestActivationHoldsPollLockAcrossCommit(t *testing.T) {
	s, stream := activationStudio(t, studioScore)
	polled := make(chan struct{})
	response := httptest.NewRecorder()
	s.applyWithHook(response, studioEdit{Revision: studioRevision([]byte(studioScore))}, func(source []byte) ([]byte, error) {
		return bytes.Replace(source, []byte("track bass acid {}"), []byte("track bass acid { level=-3dB }"), 1), nil
	}, func() {
		if s.transport.pollMu.TryLock() {
			s.transport.pollMu.Unlock()
			t.Error("poll lock was not held across the file exchange")
		}
		go func() { s.transport.poll(); close(polled) }()
	})
	<-polled
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	renderStudioPreview(t, stream, 256)
	if stream.LandedRevision() == 0 || s.transport.snapshot().Pending {
		t.Fatal("activation did not suppress Offer")
	}
}

func TestActivationFallsBackForStructureAndPendingOffer(t *testing.T) {
	s, stream := activationStudio(t, studioScore)
	added := strings.Replace(studioScore, "track bass acid {}", "track bass acid {}\ntrack lead acid {}", 1)
	response := httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func([]byte) ([]byte, error) { return []byte(added), nil })
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	s.transport.poll()
	if !s.transport.snapshot().Pending {
		t.Fatal("structural commit did not offer")
	}
	response = httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func(source []byte) ([]byte, error) {
		return bytes.Replace(source, []byte("track bass acid {}"), []byte("track bass acid { level=-3dB }"), 1), nil
	})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	renderStudioPreview(t, stream, 256)
	if stream.LandedRevision() != 0 {
		t.Fatal("patched against an offered engine before it landed")
	}
	s.transport.poll()
}

func TestActivationArenaPressureFallsBackWithoutMarkingRevision(t *testing.T) {
	s, stream := activationStudio(t, studioScore)
	var reserved []*liveplay.Patch
	for batch := s.transport.arena.Begin(99); batch != nil; batch = s.transport.arena.Begin(99) {
		reserved = append(reserved, batch)
	}
	defer func() {
		for _, batch := range reserved {
			batch.Release()
		}
	}()
	before := s.transport.last
	response := httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func(source []byte) ([]byte, error) {
		return bytes.Replace(source, []byte("track bass acid {}"), []byte("track bass acid { level=-3dB }"), 1), nil
	})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	if s.transport.last != before {
		t.Fatal("failed publication suppressed the watcher")
	}
	s.transport.poll()
	if !s.transport.snapshot().Pending || stream.LandedRevision() != 0 {
		t.Fatal("arena pressure did not use Offer")
	}
}

func TestActivationQueuesSuccessiveCommitsBeforeRender(t *testing.T) {
	s, stream := activationStudio(t, studioScore)
	for _, replacement := range []string{"track bass acid { level=-3dB }", "track bass acid { level=0dB }"} {
		response := httptest.NewRecorder()
		s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func([]byte) ([]byte, error) {
			return []byte(strings.Replace(studioScore, "track bass acid {}", replacement, 1)), nil
		})
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
	}
	if stream.LandedRevision() != 0 {
		t.Fatal("commit applied on the control thread")
	}
	renderStudioPreview(t, stream, 256)
	if stream.LandedRevision() != 2 {
		t.Fatal("successive commits did not land in order")
	}
	if got, ok := stream.CommittedValue(0, kernel.ParamMixGain); !ok || got != 0 {
		t.Fatalf("latest commit %g/%v", got, ok)
	}
	s.transport.poll()
	if s.transport.snapshot().Pending {
		t.Fatal("watcher offered after queued commits")
	}
}

// A toggled drum step and a toggled melodic step land as patches, without a
// replacement engine.
func TestActivationPatchesToggledStepsOfEveryKindWithoutReplacement(t *testing.T) {
	for _, test := range []struct {
		name, pattern, lane string
	}{{"drum", "beat", "bd"}, {"acid", "pulse", ""}} {
		t.Run(test.name, func(t *testing.T) {
			s, stream := activationStudio(t, studioScore)
			handler := s.domainRoutes()
			renderStudioPreview(t, stream, 256)
			response := studioCall(t, handler, "/api/toggle", studioEdit{Revision: studioRevision(s.lastGoodSource), Pattern: test.pattern, Lane: test.lane, Step: 1})
			if response.Code != http.StatusOK {
				t.Fatalf("toggle: %d %s", response.Code, response.Body.String())
			}
			if stream.LandedRevision() != 0 {
				t.Fatal("the toggle landed a patch on the control thread")
			}
			renderStudioPreview(t, stream, 256)
			s.transport.poll()
			if stream.LandedRevision() != 1 || s.transport.snapshot().Pending {
				t.Fatalf("toggled %s step did not patch (revision %d, pending %v)", test.name, stream.LandedRevision(), s.transport.snapshot().Pending)
			}
		})
	}
}

func TestActivationPatchesSceneBindingWithoutReplacement(t *testing.T) {
	source := "cicada 2\ntrack bass acid {}\npattern a acid steps=4 { 1 . 5 . }\npattern b acid steps=4 { 5 . 1 . }\nscene s1 { bass=a }\nscene s2 { bass=b }\nscene s3 { bass=b }\nsong { s1 s2 s3 }\n"
	s, stream := activationStudio(t, source)
	renderStudioPreview(t, stream, 256)
	response := httptest.NewRecorder()
	s.apply(response, studioEdit{Revision: studioRevision(s.lastGoodSource)}, func(source []byte) ([]byte, error) {
		return bytes.Replace(source, []byte("scene s3 { bass=b }"), []byte("scene s3 { bass=a }"), 1), nil
	})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	renderStudioPreview(t, stream, 256)
	if stream.LandedRevision() != 1 || s.transport.snapshot().Pending {
		t.Fatalf("scene binding did not patch (revision %d, pending %v)", stream.LandedRevision(), s.transport.snapshot().Pending)
	}
}

func TestHealthFrameCarriesThePlayerCPULoad(t *testing.T) {
	s, stream := activationStudio(t, studioScore)
	renderStudioPreview(t, stream, 256*20)
	s.transport.mu.Lock()
	s.transport.publishHealthLocked()
	s.transport.mu.Unlock()
	health, err := liveplay.DecodeHealthFrame(s.transport.telemetry.Latest()[liveplay.FrameHealth])
	if err != nil || !(health.CPULoad > 0) || health.CPULoad != float32(stream.CPULoad()) {
		t.Fatalf("health frame %+v (%v), player load %g", health, err, stream.CPULoad())
	}
}
