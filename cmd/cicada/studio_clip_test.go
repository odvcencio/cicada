package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"m31labs.dev/cicada/internal/testwav"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const clipEditScore = "cicada 2\nasset take \"take.wav\" { sha256 = \"" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + "\" format = wav frames = 48000 rate = 48000Hz channels = 1 }\nclip hit take { start = 10ms // original region\n end = 100ms gain = -2.0dB } // keep clip comment\ntrack vox audio {}\nscene main { vox = hit }\nsong { main }\n"

func TestClipRegionPatchesPreserveCommentsAndUnchangedUnits(t *testing.T) {
	before := []byte(strings.ReplaceAll(clipEditScore, "\n", "\r\n"))
	settings := &studioClipSettings{Start: 480, End: 4800, GainDB: -2, FadeIn: 48, FadeOut: 96}
	updated, err := clipSettingsSource(before, "hit", settings, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"start = 10ms // original region\r\n", "end = 100ms gain = -2.0dB", "// keep clip comment\r\n", "fade_in = 48frames", "fade_out = 96frames"} {
		if !bytes.Contains(updated, []byte(text)) {
			t.Fatalf("lost %q: %s", text, updated)
		}
	}
	if bytes.Count(updated, []byte("\n")) != bytes.Count(updated, []byte("\r\n")) {
		t.Fatal("CRLF changed")
	}
	settings.End = 50000
	if _, err := clipSettingsSource(before, "hit", settings, 2); err == nil {
		t.Fatal("clip extends past asset")
	}
	settings.End = 481
	settings.FadeIn = 48
	if _, err := clipSettingsSource(before, "hit", settings, 2); err == nil {
		t.Fatal("fade extends past region")
	}
}

func TestCreateAudioTrackAndAssignClip(t *testing.T) {
	before := []byte(clipEditScore)
	updated, err := newAudioTrackSource(before, "vocal-b", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("track vocal-b audio {}")) || !bytes.Contains(updated, []byte("// keep clip comment")) {
		t.Fatal("new track lost source")
	}
	updated, err = bindPatternSource(updated, "main", "vocal-b", "hit")
	if err != nil {
		t.Fatal(err)
	}
	p := patternProject(t, updated)
	if p.Scenes[0].Bindings["vocal-b"] != "hit" || p.Tracks[1].Slots[0] == nil {
		t.Fatal("audio bank was not assigned")
	}
	if _, err := newAudioTrackSource(before, "vox", 2); err == nil {
		t.Fatal("duplicate track")
	}
	if _, err := newAudioTrackSource(before, "bad\ntrack x", 2); err == nil {
		t.Fatal("track declaration injection")
	}
	if _, err := newAudioTrackSource(before, "new", 1); err == nil {
		t.Fatal("implicit edition upgrade")
	}
}

func TestClipCommandsRejectStaleOrInvalidWritesAndUndo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score.cicada")
	wav := testwav.Bytes(48000, 1, 16, 48000, 1)
	if err := os.WriteFile(filepath.Join(dir, "take.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte(strings.Replace(clipEditScore, strings.Repeat("a", 64), fmt.Sprintf("%x", sha256.Sum256(wav)), 1))
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := &studioClipSettings{Start: 480, End: 4800, GainDB: -4, FadeIn: 48, FadeOut: 96}
	response := studioCall(t, handler, "/api/clip", studioEdit{Revision: studioRevision(source), Action: "clip-settings", Pattern: "hit", ClipSettings: settings})
	if response.Code != 200 {
		t.Fatalf("clip edit: %d %s", response.Code, response.Body.String())
	}
	updated, _ := os.ReadFile(path)
	if stale := studioCall(t, handler, "/api/clip", studioEdit{Revision: studioRevision(source), Action: "audio-track", NewName: "stale"}); stale.Code != 409 {
		t.Fatalf("stale edit: %d", stale.Code)
	}
	settings.End = 50000
	if bad := studioCall(t, handler, "/api/clip", studioEdit{Revision: studioRevision(updated), Action: "clip-settings", Pattern: "hit", ClipSettings: settings}); bad.Code != 422 {
		t.Fatalf("invalid region: %d", bad.Code)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(updated, unchanged) {
		t.Fatal("rejected audio edit changed disk")
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(updated)})
	restored, _ := os.ReadFile(path)
	if undo.Code != 200 || !bytes.Equal(restored, source) {
		t.Fatal("audio region undo did not restore exact source")
	}
}
