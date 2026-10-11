package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
)

func activationStudio(t *testing.T, source string) (*studio, *liveplay.Player) {
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
	return &studio{path: path, lastGoodSource: []byte(source), lastGoodProject: p, transport: transport}, stream
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
