package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestPreviewRemappedKitCompiledControlsRejectRecipeInvalidValues(t *testing.T) {
	for _, path := range []string{"scene", "automation"} {
		t.Run(path, func(t *testing.T) {
			score, diagnostics := notation.Parse([]byte("cicada 2\ntempo 120\nkit acoustic { bd=builtin.sd }\ntrack bass acoustic {}\npattern beat drums steps=2 { bd:x. }\nscene main { bass=beat }\nsong { main }\n"))
			if hasErrors(diagnostics) {
				t.Fatal(diagnostics)
			}
			p, diagnostics := FromScore(score)
			if hasErrors(diagnostics) {
				t.Fatal(diagnostics)
			}
			p.Format, p.Version = FormatID2, 2
			value := float64(55)
			setting := SceneValue{Unit: "hz", Number: &value}
			if path == "scene" {
				p.Scenes[0].Settings = []SceneSetting{{Path: "bass.bd_tune", Value: setting}}
			} else {
				p.Automation = []AutomationLane{{Path: "bass.bd_tune", Points: []AutomationPoint{{Tick: 0, Value: setting, Shape: "step"}}}}
			}
			if err := ValidateProject(p); err != nil {
				t.Fatalf("fixture failed before prepared recipe checks: %v", err)
			}
			if _, err := CompileEngine(p, 48_000, 256); err == nil || !strings.Contains(err.Error(), "bass.bd_tune") {
				t.Fatalf("compiled invalid %s control: %v", path, err)
			}
			// The same mapping still exposes controls in both recipes' ranges.
			value = 100
			setting = SceneValue{Unit: "ms", Number: &value}
			if path == "scene" {
				p.Scenes[0].Settings[0] = SceneSetting{Path: "bass.bd_decay", Value: setting}
			} else {
				p.Automation[0].Path = "bass.bd_decay"
				p.Automation[0].Points[0].Value = setting
			}
			cfg, err := CompileEngine(p, 48_000, 256)
			if err != nil {
				t.Fatalf("valid %s control rejected: %v", path, err)
			}
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
				t.Fatal("play command rejected")
			}
			var left, right [256]float32
			e.Render(left[:], right[:])
			var message cmd.Message
			for e.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatalf("valid compiled %s control faulted: %d", path, message.A)
				}
			}
			if !e.Playing() {
				t.Fatalf("valid compiled %s control stopped playback", path)
			}
		})
	}
}
