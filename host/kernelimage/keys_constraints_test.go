package kernelimage_test

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	hostkeys "m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var keysConstraintPatches = map[string]string{"ep": "tine_ep", "clav": "clav", "organ": "organ_full", "fm": "fm_ep", "analog": "poly_keys", "strings": "string_machine"}

func keysConstraintText(d paramdefs.Descriptor, v float64) string {
	if d.Curve == "toggle" && (v == 0 || v == 1) {
		return map[float64]string{0: "off", 1: "on"}[v]
	}
	text := strconv.FormatFloat(v, 'g', -1, 64)
	switch strings.ToLower(d.Unit) {
	case "hz":
		return text + "hz"
	case "db":
		return text + "db"
	}
	return text
}

// Preview admission, scene compile, automation compile, image load and the
// engine's own command path must agree on every keyboard control: the same
// value is accepted by all of them or refused by all of them.
func TestKeyboardControlConstraintsAgreeAcrossEveryPath(t *testing.T) {
	checked := 0
	for _, d := range paramdefs.Registry {
		if !strings.HasPrefix(d.ID, "keys.") {
			continue
		}
		patch := keysConstraintPatches[d.Voices[0]]
		id, _ := kernel.FindParam(d.ID)
		span := d.Max - d.Min
		values := []float64{d.Min, d.Max, d.Default, d.Min + span/2, d.Min - span/100, d.Max + span/100, d.Min + .5, d.Max - .25, .5, -1}
		for _, v := range values {
			name := fmt.Sprintf("%s=%g", d.ID, v)
			// Preview admission (detached validator, as liveplay uses).
			var validator engine.PreviewParamValidator
			preview := validator.Validate(id, float32(v)) == nil
			// Scene setting compile.
			source := fmt.Sprintf("cicada 2\ntrack keys %s {}\npattern p notes { c4 }\nscene main { keys=p keys.%s = %s }\nsong { main }\n", patch, d.Path, keysConstraintText(d, v))
			scene := false
			if score, ds := notation.Parse([]byte(source)); !errorsIn(ds) {
				if p, ds := project.FromScore(score); p != nil && !errorsIn(ds) {
					scene = project.ValidateProject(p) == nil
				}
			}
			// Image load.
			cfg := keysImageConfig()
			spec, err := hostkeys.DefaultSpec(patch)
			if err != nil {
				t.Fatal(err)
			}
			spec.Controls[127] = 8
			cfg.Track[0].Keys = &spec
			cfg.Scenes = []engine.Scene{{Settings: []engine.SceneSetting{{Track: 0, ID: id, Value: float32(v)}}}}
			cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}}
			_, imageErr := kernelimage.Encode(cfg)
			image := imageErr == nil
			// Engine command path.
			e, engineErr := engine.New(cfg)
			if engineErr != nil {
				image = false // Config itself refused.
			}
			command := false
			if e != nil {
				e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(id), Arg0: math.Float32bits(float32(v))})
				var l, r [128]float32
				e.Render(l[:], r[:])
				var m cmd.Message
				command = true
				for e.Poll(&m) {
					if m.Kind == cmd.Fault {
						command = false
					}
				}
			}
			if preview != scene || scene != image || image != command {
				t.Errorf("%s: preview=%v scene=%v image=%v engine=%v", name, preview, scene, image, command)
			}
			// Automation compile, for controls automation may move.
			if d.Automatable {
				lane := fmt.Sprintf("cicada 2\ntrack keys %s {}\npattern p notes { c4 }\nautomate keys.%s { @1.1.1 %s @2.1.1 %s }\nscene main { keys=p }\nsong { main*2 }\n", patch, d.Path, keysConstraintText(d, v), keysConstraintText(d, v))
				automation := false
				if score, ds := notation.Parse([]byte(lane)); !errorsIn(ds) {
					if p, ds := project.FromScore(score); p != nil && !errorsIn(ds) {
						_, cerr := project.CompileAutomation(p)
						automation = project.ValidateProject(p) == nil && cerr == nil
					}
				}
				if automation != preview {
					t.Errorf("%s: automation=%v but preview=%v", name, automation, preview)
				}
			}
			checked++
		}
	}
	if checked < 200 {
		t.Fatalf("only %d cases", checked)
	}
	t.Logf("%d value cases agreed across preview, scene, automation, image and engine", checked)
}

func errorsIn(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
