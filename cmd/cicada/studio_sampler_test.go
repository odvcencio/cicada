package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/host/padsampler"
)

func samplerStudio(t *testing.T) (*studio, http.Handler) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(path, []byte(studioListeningScore), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.transport.close)
	return s, s.routes()
}

func samplerSnapshot(t *testing.T, h http.Handler) (string, string, padsampler.Bank, []string) {
	t.Helper()
	w := studioCall(t, h, "/api/sampler", nil)
	var state struct {
		Revision, BankRevision string
		Bank                   padsampler.Bank
		Sounds                 []string
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state.Revision, state.BankRevision, state.Bank, state.Sounds
}

func samplerCommandCall(t *testing.T, h http.Handler, c samplerCommand) *httptest.ResponseRecorder {
	t.Helper()
	c.Revision, c.BankRevision, _, _ = samplerSnapshot(t, h)
	return studioCall(t, h, "/api/sampler", c)
}

func TestStudioSamplerPlacementResampleAndAtomicUndo(t *testing.T) {
	s, h := samplerStudio(t)
	w := samplerCommandCall(t, h, samplerCommand{Action: "sample-placement", Placement: "pickup", Count: 4})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	rev, bankRev, b, ids := samplerSnapshot(t, h)
	if len(b.Sources) != 1 || b.Pads[3].End != b.Sources[0].Asset.Frames || b.Pads[4].Source != "" {
		t.Fatal("incorrect placement chops")
	}
	peak := float32(0)
	for _, p := range b.Sources[0].Peaks {
		peak = max(peak, p)
	}
	if peak < .7 {
		t.Fatalf("placement sampler produced silence: peak %f", peak)
	}
	w = studioCall(t, h, "/api/sampler/sound/"+ids[0], nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if a, err := padsampler.Decode(w.Body.Bytes()); err != nil || len(a.Left) == 0 {
		t.Fatalf("pad media: %v", err)
	}
	beforeBank, _ := s.padStore()
	_, beforeBytes, _ := beforeBank.Read()
	w = studioCall(t, h, "/api/sampler", samplerCommand{Action: "resample", Revision: rev, BankRevision: bankRev, Performance: padsampler.Performance{Frames: 96000, Hits: []padsampler.Hit{{Sound: ids[0], Pad: 1, Velocity: 1, Choke: 1}}}, NewName: "pad-print", Track: "sampler", At: "@3.1.1", Destination: 16, Place: true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	source, _ := os.ReadFile(s.path)
	if !bytes.Contains(source, []byte("place pad-print sampler pad-print-clip")) || !bytes.HasPrefix(source, []byte(studioListeningScore[:len(studioListeningScore)-2])) {
		t.Fatal("resample did not preserve score and append a placement")
	}
	p, err := compileStudioSource(s.path, source)
	if err != nil || len(p.Arrange.Placements) != 3 || len(p.Clips) != 1 {
		t.Fatalf("resampled arrangement: %v", err)
	}
	_, _, printed, printIDs := samplerSnapshot(t, h)
	if !printed.Pads[15].BypassEffects || len(printed.Performance.Hits) != 1 || printIDs[15] == "" {
		t.Fatal("print did not become a playable bypassed pad")
	}
	w = studioCall(t, h, "/api/undo", studioEdit{Revision: studioRevision(source)})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	restored, _ := os.ReadFile(s.path)
	_, restoredBank, _ := beforeBank.Read()
	if !bytes.Equal(restored, []byte(studioListeningScore)) || !bytes.Equal(restoredBank, beforeBytes) {
		t.Fatal("Undo did not restore both score and bank")
	}
}

func TestStudioSamplerRejectsStaleInvalidAndUnpreparedEdits(t *testing.T) {
	s, h := samplerStudio(t)
	rev, bankRev, _, _ := samplerSnapshot(t, h)
	for _, c := range []samplerCommand{{Action: "sample-placement", Revision: "old", BankRevision: bankRev, Placement: "bed", Count: 1}, {Action: "effects", Revision: rev, BankRevision: "old", Effects: ptrEffects(padsampler.Clean())}} {
		if w := studioCall(t, h, "/api/sampler", c); w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := samplerCommandCall(t, h, samplerCommand{Action: "resample", Performance: padsampler.Performance{Frames: 4800, Hits: []padsampler.Hit{{Sound: strings.Repeat("a", 64), Pad: 1, Velocity: 1}}}, NewName: "bad", Destination: 16}); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	if source, _ := os.ReadFile(s.path); !bytes.Equal(source, []byte(studioListeningScore)) {
		t.Fatal("rejected sampler command changed the score")
	}
	if w := samplerCommandCall(t, h, samplerCommand{Action: "sample-placement", Placement: "missing", Count: 16}); w.Code != 422 {
		t.Fatal(w.Code)
	}
}

func ptrEffects(e padsampler.Effects) *padsampler.Effects { return &e }

func TestSamplerAddsAudioTrackWithoutRevalidatingUnresolvedImports(t *testing.T) {
	source := []byte("cicada 2\nimport \"std/keys\"\ntrack keys keys.tine_ep {}\npattern head { 1 . 3 . }\narrange { place original keys head { at = @1.1.1 length = 4bars } }\n")
	updated, err := newAudioTrackSource(source, "sampler", 2)
	if err != nil || !bytes.HasPrefix(updated, source) || !bytes.Contains(updated, []byte("track sampler audio {}")) {
		t.Fatalf("imported score cannot acquire an audio track: %v", err)
	}
	if _, err = newAudioTrackSource(updated, "sampler", 2); err == nil {
		t.Fatal("duplicate local audio track accepted")
	}
}

func TestSamplerPrintClipCanBeEditedWithImportedInstruments(t *testing.T) {
	source := []byte("cicada 2\nimport \"std/keys\"\ntrack keys keys.tine_ep {}\nasset printed-audio \"sample.wav\" { sha256 = \"" + strings.Repeat("0", 64) + "\" format = wav frames = 48000 rate = 48000Hz channels = 2 }\nclip printed printed-audio { start = 0frames end = 48000frames }\n")
	updated, err := clipSettingsSource(source, "printed", &studioClipSettings{End: 44100, GainDB: -3, FadeIn: 120, FadeOut: 240}, 2)
	if err != nil || !bytes.Contains(updated, []byte("end = 44100frames")) || !bytes.Contains(updated, []byte("gain = -3dB")) {
		t.Fatalf("imported score cannot edit a printed clip: %v", err)
	}
}

func TestStudioSamplerMultipartImportPadEditsAndBankUndo(t *testing.T) {
	s, h := samplerStudio(t)
	rev, bankRev, _, _ := samplerSnapshot(t, h)
	a := audition.Audio{Rate: 48000, Left: make([]float32, 1600), Right: make([]float32, 1600)}
	a.Left[300] = .3
	a.Right[600] = -.2
	wav, _ := padsampler.WAV(a)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("revision", rev)
	_ = form.WriteField("bankRevision", bankRev)
	file, _ := form.CreateFormFile("audio", "stereo.wav")
	_, _ = file.Write(wav)
	_ = form.Close()
	r := httptest.NewRequest("POST", "/api/sampler/import", &body)
	r.Host = "127.0.0.1:1234"
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, _, b, _ := samplerSnapshot(t, h)
	p := b.Pads[0]
	p.End = 1601
	if w = samplerCommandCall(t, h, samplerCommand{Action: "pad", Pad: 1, Settings: &p}); w.Code != 422 {
		t.Fatal(w.Code)
	}
	p = b.Pads[0]
	p.Reverse = true
	p.Mode = "gate"
	p.Pitch = -12
	if w = samplerCommandCall(t, h, samplerCommand{Action: "pad", Pad: 1, Settings: &p}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = samplerCommandCall(t, h, samplerCommand{Action: "undo"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, _, restored, _ := samplerSnapshot(t, h)
	if padsampler.Revision(restored) != padsampler.Revision(b) {
		t.Fatal("pad undo changed other settings")
	}
	if source, _ := os.ReadFile(s.path); !bytes.Equal(source, []byte(studioListeningScore)) {
		t.Fatal("pad editing rewrote the score")
	}
}
