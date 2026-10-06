package demobrowser

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

func TestPresetAudio(t *testing.T) {
	for _, preset := range Presets() {
		for _, rate := range []int{44100, 48000} {
			t.Run(preset.ID+"/"+strconv.Itoa(rate), func(t *testing.T) {
				_, cfg, image, err := Prepare([]byte(preset.Source), rate)
				if err != nil {
					t.Fatal(err)
				}
				if len(image) < 32 {
					t.Fatal("empty kernel image")
				}
				e, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
					t.Fatal("Play rejected")
				}
				left, right := make([]float32, 128), make([]float32, 128)
				energy := 0.0
				for block := 0; block < rate/128; block++ {
					e.Render(left, right)
					for _, sample := range left {
						v := float64(sample)
						if math.IsNaN(v) || math.IsInf(v, 0) {
							t.Fatal("nonfinite PCM")
						}
						energy += v * v
					}
				}
				if energy < 1e-7 {
					t.Fatal("silent sketch")
				}
			})
		}
	}
}

func TestSessionRejectsInvalidEditsWithoutChangingHistory(t *testing.T) {
	s, err := NewSession()
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	for _, source := range []string{"not a score", "import \"private/library\"\n" + ensemble, strings.Replace(ensemble, "620Hz", "nanHz", 1)} {
		if err := s.Apply(before.Revision, []byte(source)); err == nil {
			t.Fatal("invalid score accepted")
		}
		if after := s.Snapshot(); after.Revision != before.Revision || after.CanUndo {
			t.Fatal("invalid edit changed session")
		}
	}
	if err := s.Apply(before.Revision, []byte(Presets()[1].Source)); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(s.Snapshot().Revision); err != nil {
		t.Fatal(err)
	}
	if string(s.Snapshot().Source) != string(before.Source) {
		t.Fatal("undo did not restore score")
	}
	if err := s.Redo(s.Snapshot().Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(s.Snapshot().Revision); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().CanUndo || s.Snapshot().CanRedo {
		t.Fatal("reset retained discarded edits")
	}
}
