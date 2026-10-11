package edit_test

import (
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

func TestSelectTakeExactBytesAndErrors(t *testing.T) {
	source := "cicada 2\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { vox = off bass = pulse }\nsong { main }\n"
	in := edits.SelectTake{ID: "take-fixed", Track: "vox", Scene: "main", Rate: 48000, Channels: 1, StartFrame: 4, Asset: edits.TakeAsset{Name: "take-fixed", Path: "take.wav", SHA256: strings.Repeat("a", 64), Frames: 10, RateHz: 48000, Channels: 1}}
	suffix := "\nasset take-fixed \"take.wav\" {\n  sha256 = \"" + strings.Repeat("a", 64) + "\"\n  format = wav\n  frames = 10\n  rate = 48000Hz\n  channels = 1\n  source = recorded\n}\n\nclip take-fixed-clip take-fixed {\n  start = 4frames\n  end = 10frames\n}\n"
	for _, newline := range []string{"\n", "\r\n"} {
		before := strings.ReplaceAll(source, "\n", newline)
		want := strings.Replace(before, "vox = off", "vox = take-fixed-clip", 1) + suffix
		got, err := edits.SelectTakeSource([]byte(before), in)
		if err != nil || string(got) != want {
			t.Fatalf("selection: %v\n%s\nwant:%s", err, got, want)
		}
		replay, err := edits.SelectTakeSource(got, in)
		if err != nil || string(replay) != want {
			t.Fatalf("replay: %v\n%s", err, replay)
		}
		collision := strings.Replace(want, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
		if _, err := edits.SelectTakeSource([]byte(collision), in); err == nil || err.Error() != "take asset name collision" {
			t.Fatalf("collision: %v", err)
		}
	}
	for _, c := range []struct {
		track, scene string
		start        int64
		want         string
	}{
		{"bass", "main", 4, "take target must be an existing audio track"},
		{"vox", "missing", 4, "take scene no longer exists"},
		{"vox", "main", 10, "take contains only preroll; retained for recovery"},
	} {
		copy := in
		copy.Track, copy.Scene, copy.StartFrame = c.track, c.scene, c.start
		if _, err := edits.SelectTakeSource([]byte(source), copy); err == nil || err.Error() != c.want {
			t.Fatalf("error: %v want %s", err, c.want)
		}
	}
}
