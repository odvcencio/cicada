package demobrowser

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/loudness"
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

func TestPresetLoudness(t *testing.T) {
	for _, preset := range Presets() {
		for _, rate := range []int{44100, 48000} {
			t.Run(preset.ID+"/"+strconv.Itoa(rate), func(t *testing.T) {
				_, cfg, _, err := Prepare([]byte(preset.Source), rate)
				if err != nil {
					t.Fatal(err)
				}
				e, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				meter, err := loudness.New(rate)
				if err != nil {
					t.Fatal(err)
				}
				e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
				left, right := make([]float32, 128), make([]float32, 128)
				frames := int(int64(rate) * 60 * 4 * 16 * 1000 / cfg.BPMMilli)
				for at := 0; at < frames; at += len(left) {
					e.Render(left, right)
					if err := meter.ProcessBlock(left, right); err != nil {
						t.Fatal(err)
					}
				}
				if err := meter.Finish(); err != nil {
					t.Fatal(err)
				}
				result := meter.Metrics()
				if math.Abs(result.IntegratedLUFS+20) > 1.5 || result.TruePeakDBTP > -1 {
					t.Fatalf("sketch loudness %.2f LUFS, true peak %.2f dBTP", result.IntegratedLUFS, result.TruePeakDBTP)
				}
			})
		}
	}
}

func TestKeyboardEditsPreserveLastGoodScore(t *testing.T) {
	s, err := NewSession()
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	for _, name := range keyboard.Names {
		source := strings.Replace(ensemble, "model_marimba", name, 1)
		if err := s.Apply(before.Revision, []byte(source)); err == nil || !strings.Contains(err.Error(), "keyboard instruments are unavailable") {
			t.Fatalf("%s: %v", name, err)
		}
		if after := s.Snapshot(); after.Revision != before.Revision || after.CanUndo {
			t.Fatal("unsupported keyboard changed the last good score")
		}
	}
	if _, _, _, err := Prepare(before.Source, 48000); err != nil {
		t.Fatal("last good score cannot play:", err)
	}
	// A local graph instrument can use the same name as a keyboard patch.
	source := strings.Replace(ensemble, "instrument glass", "instrument tine_ep", 1)
	source = strings.ReplaceAll(source, " glass ", " tine_ep ")
	if _, err := Parse([]byte(source)); err != nil {
		t.Fatal("authored synth mistaken for a keyboard:", err)
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
