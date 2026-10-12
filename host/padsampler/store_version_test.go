//go:build !js || !wasm

package padsampler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/project"
)

// Keep the original wire hash here independently of the current preparation
// version, so compatibility cannot pass by accidentally testing the new hash.
func legacyPreparedSoundID(p Pad, e Effects, src Source) string {
	if p.BypassEffects {
		e = Clean()
	}
	data, _ := json.Marshal(struct {
		Version int
		Pad     Pad
		Effects Effects
		Source  string
	}{1, p, e, src.ID})
	return Digest(data)
}

func TestPreparedSoundVersionPreservesLegacyRecordings(t *testing.T) {
	s := NewStore(t.TempDir(), "main.cicada")
	data, err := WAV(testAudio(2400))
	if err != nil {
		t.Fatal(err)
	}
	src, err := s.Import(data, "original")
	if err != nil {
		t.Fatal(err)
	}
	p := Pad{Name: "recorded", Source: src.ID, End: 2400, GainDB: -6, Mode: "oneshot", ReleaseMS: 5}
	effects := Clean()
	effects.Drive, effects.DelayMix = 9, .2
	legacyID := legacyPreparedSoundID(p, effects, src)
	legacy := Sound{ID: legacyID, Pad: p, Effects: effects, Source: src,
		Asset: project.Asset{Name: "pad-" + legacyID[:12], Path: "sampler/sounds/" + legacyID + ".wav", SHA256: Digest(data), Format: "wav", Frames: 2400, RateHz: Rate, Channels: 2}}
	legacyMeta, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacyMeta, []byte("processingVersion")) {
		t.Fatal("legacy fixture must omit processingVersion")
	}
	metaPath := "sampler/sounds/" + legacyID + ".json"
	if err := s.Write(legacy.Asset.Path, data, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(metaPath, legacyMeta, false); err != nil {
		t.Fatal(err)
	}
	bank := DefaultBank()
	bank.Sources, bank.Pads[0] = []Source{src}, p
	bank.Performance = Performance{Frames: 2400, Hits: []Hit{{Sound: legacyID, Pad: 0, Velocity: 1}}}
	if err := s.Write(s.BankPath, BankBytes(bank), true); err != nil {
		t.Fatal(err)
	}
	storedBank, _, err := s.Read()
	if err != nil || storedBank.Version != 1 || storedBank.Performance.Hits[0].Sound != legacyID {
		t.Fatalf("legacy bank or performance reference changed: %v", err)
	}
	old, err := s.Sound(legacyID)
	if err != nil || old.ProcessingVersion != 1 || old.ID != legacyID {
		t.Fatalf("legacy prepared metadata rejected: %+v, %v", old, err)
	}
	if _, err := s.Audio(old.Asset); err != nil {
		t.Fatalf("legacy WAV is unreadable: %v", err)
	}
	prepared, err := s.Prepare(p, effects, src)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ProcessingVersion != 2 || prepared.ID == legacyID || prepared.ID != SoundID(p, effects, src) {
		t.Fatalf("preparation reused the legacy processing version: %+v", prepared)
	}
	preparedMeta, err := os.ReadFile(filepath.Join(s.Root, "sampler/sounds/"+prepared.ID+".json"))
	if err != nil || !bytes.Contains(preparedMeta, []byte(`"processingVersion":2`)) {
		t.Fatalf("new cache lacks explicit processing version: %s, %v", preparedMeta, err)
	}
	// A second preparation must use the new cache, without reloading/reprocessing
	// its source. The already-recorded legacy WAV is independent of that source.
	if err := os.Remove(filepath.Join(s.Root, src.Asset.Path)); err != nil {
		t.Fatal(err)
	}
	again, err := s.Prepare(p, effects, src)
	if err != nil || again.ID != prepared.ID || again.ProcessingVersion != 2 {
		t.Fatalf("new processing cache was not reused: %+v, %v", again, err)
	}
	for path, want := range map[string][]byte{legacy.Asset.Path: data, metaPath: legacyMeta} {
		got, err := os.ReadFile(filepath.Join(s.Root, path))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("immutable legacy file %s changed: %v", path, err)
		}
	}
	if _, err := s.Audio(old.Asset); err != nil {
		t.Fatalf("recorded legacy asset stopped playing after new preparation: %v", err)
	}
}

func TestPreparedSoundRejectsUnsupportedOrTamperedVersion(t *testing.T) {
	s := NewStore(t.TempDir(), "main.cicada")
	data, _ := WAV(testAudio(2400))
	src, err := s.Import(data, "source")
	if err != nil {
		t.Fatal(err)
	}
	p := Pad{Name: "pad", Source: src.ID, End: 2400, Mode: "oneshot", ReleaseMS: 5}
	sound, err := s.Prepare(p, Clean(), src)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(sound)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, "sampler/sounds/"+sound.ID+".json")
	for _, value := range []string{"0", "-1", "3", "null", `"2"`, "2.5", "1", "missing"} {
		t.Run(value, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(metadata, &fields); err != nil {
				t.Fatal(err)
			}
			if value == "missing" {
				delete(fields, "processingVersion")
			} else {
				fields["processingVersion"] = json.RawMessage(value)
			}
			changed, _ := json.Marshal(fields)
			if err := os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Sound(sound.ID); err == nil {
				t.Fatalf("version tampering %s was accepted", value)
			}
		})
	}
	// A valid current version still authenticates the original pad settings.
	sound.Pad.GainDB = -12
	changed, _ := json.Marshal(sound)
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sound(sound.ID); err == nil {
		t.Fatal("altered preparation settings were accepted")
	}
}
