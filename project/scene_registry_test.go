package project

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

type sceneRegistryValueForm struct {
	name   string
	source string
}

func TestLiveSceneRegistryValueFormsValidateAndCompile(t *testing.T) {
	cases := 0
	for _, descriptor := range paramdefs.Registry {
		if !descriptor.Live || !sceneRegistryDescriptorSettable(descriptor) {
			continue
		}
		forms := []sceneRegistryValueForm{
			{name: "min", source: sceneRegistryNumber(descriptor, descriptor.Min)},
			{name: "default", source: sceneRegistryNumber(descriptor, descriptor.Default)},
			{name: "max", source: sceneRegistryNumber(descriptor, descriptor.Max)},
		}
		for _, value := range descriptor.Values {
			forms = append(forms, sceneRegistryValueForm{name: "enum-" + value, source: value})
		}
		if descriptor.Curve == "toggle" {
			forms = append(forms,
				sceneRegistryValueForm{name: "toggle-on", source: "on"},
				sceneRegistryValueForm{name: "toggle-off", source: "off"},
			)
		}
		if descriptor.Off {
			forms = append(forms, sceneRegistryValueForm{name: "off", source: "off"})
		}
		for _, form := range forms {
			form := form
			cases++
			t.Run(descriptor.ID+"/"+form.name, func(t *testing.T) {
				score, diagnostics := notation.Parse([]byte(sceneRegistryScore(descriptor, form.source)))
				for _, diagnostic := range diagnostics {
					if diagnostic.Severity == "error" {
						t.Fatalf("score validation: %+v", diagnostic)
					}
				}
				p, diagnostics := FromScore(score)
				if p == nil {
					t.Fatalf("semantic validation: %+v", diagnostics)
				}
				if err := ValidateProject(p); err != nil {
					t.Fatalf("ValidateProject rejected a live registry value: %v", err)
				}
				cfg, err := CompileEngine(p, 48_000, 128)
				if err != nil {
					t.Fatalf("engine compilation rejected a validated live registry value: %v", err)
				}
				e, err := engine.New(cfg)
				if err != nil {
					t.Fatalf("engine rejected a validated live registry value: %v", err)
				}
				if !e.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 0}) {
					t.Fatal("engine rejected the one-scene launch")
				}
				var left, right [128]float32
				e.Render(left[:], right[:])
				var message cmd.Message
				for e.Poll(&message) {
					if message.Kind == cmd.Fault {
						t.Fatalf("engine faulted while applying a validated scene setting: %d", message.A)
					}
				}
			})
		}
	}
	if cases == 0 {
		t.Fatal("parameter registry has no live scene-settable values")
	}
	t.Logf("validated and compiled %d registry scene-setting cases", cases)
}

func TestSceneFloat32BoundaryNudgesOnlyExactBoundaryValue(t *testing.T) {
	var descriptor paramdefs.Descriptor
	for _, candidate := range paramdefs.Registry {
		if candidate.ID == "drum.sd.tune" {
			descriptor = candidate
			break
		}
	}
	if descriptor.ID == "" {
		t.Fatal("drum.sd.tune is missing from the parameter registry")
	}

	score, diagnostics := notation.Parse([]byte(sceneRegistryScore(descriptor, sceneRegistryNumber(descriptor, descriptor.Min))))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("score validation: %+v", diagnostic)
		}
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("semantic validation: %+v", diagnostics)
	}
	if len(p.Scenes) != 1 || len(p.Scenes[0].Settings) != 1 {
		t.Fatalf("expected one scene setting, got %+v", p.Scenes)
	}

	compiled := float32(descriptor.Min)
	if float64(compiled) >= descriptor.Min {
		t.Fatalf("test minimum does not round below its exact boundary: min=%.17g compiled=%.9g", descriptor.Min, compiled)
	}
	if err := ValidateProject(p); err != nil {
		t.Fatalf("exact range boundary was rejected: %v", err)
	}
	cfg, err := CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatalf("exact range boundary did not compile: %v", err)
	}
	want := math.Nextafter32(compiled, float32(math.Inf(1)))
	if got := cfg.Scenes[0].Settings[0].Value; got != want {
		t.Fatalf("scene minimum was not moved inward by exactly one float32 step: got %.9g want %.9g", got, want)
	}

	inside := math.Nextafter(descriptor.Min, math.Inf(1))
	p.Scenes[0].Settings[0].Value.Number = &inside
	if err := ValidateProject(p); err == nil {
		t.Fatal("non-boundary value that rounds below the float32 range was accepted")
	}

	outside := math.Nextafter(descriptor.Min, math.Inf(-1))
	p.Scenes[0].Settings[0].Value.Number = &outside
	if err := ValidateProject(p); err == nil {
		t.Fatal("out-of-range value was accepted after float32 conversion")
	}
}

func sceneRegistryDescriptorSettable(descriptor paramdefs.Descriptor) bool {
	switch descriptor.Scope {
	case "track":
		return len(descriptor.Voices) != 0
	case "global":
		return strings.HasPrefix(descriptor.ID, "fx.") && strings.Count(descriptor.Path, ".") != 0
	default:
		return false
	}
}

func sceneRegistryScore(descriptor paramdefs.Descriptor, value string) string {
	var source strings.Builder
	if descriptor.Scope == "global" {
		parts := strings.SplitN(descriptor.ID, ".", 3)
		fmt.Fprintf(&source, "fx %s {}\n", parts[1])
	}
	if strings.HasPrefix(descriptor.ID, "guitar.") {
		return fmt.Sprintf("cicada 2\ntrack lane guitar { experimental = on }\npattern riff steps=1 { 1 }\nscene main { lane = riff lane.%s = %s }\nsong { main }\n", descriptor.Path, value)
	}
	trackKind := "acid"
	patternKind := "acid"
	if descriptor.Scope == "track" && !sceneRegistryHasVoice(descriptor, "acid") {
		if sceneRegistryHasVoice(descriptor, "drums") {
			trackKind, patternKind = "drums", "drums"
		} else if sceneRegistryHasVoice(descriptor, "piano") {
			trackKind, patternKind = "piano", "notes"
		} else {
			trackKind, patternKind = "voice", "notes"
			fmt.Fprintln(&source, "instrument voice { voice mono { out = sine(pitch) * env(gate, 120ms) } }")
		}
	}
	fmt.Fprintf(&source, "track lane %s {}\n", trackKind)
	if patternKind == "drums" {
		lane := strings.Split(descriptor.ID, ".")[1]
		fmt.Fprintf(&source, "pattern riff drums steps=1 { %s: X }\n", lane)
	} else if trackKind == "piano" {
		fmt.Fprintln(&source, "pattern riff notes steps=1 { c4 }")
	} else {
		fmt.Fprintf(&source, "pattern riff %s steps=1 { 1 }\n", patternKind)
	}
	path := descriptor.Path
	if descriptor.Scope == "track" {
		path = "lane." + path
	}
	fmt.Fprintf(&source, "scene main { lane = riff %s = %s }\nsong { main }\n", path, value)
	return source.String()
}

func sceneRegistryHasVoice(descriptor paramdefs.Descriptor, voice string) bool {
	for _, candidate := range descriptor.Voices {
		if candidate == voice {
			return true
		}
	}
	return false
}

func sceneRegistryNumber(descriptor paramdefs.Descriptor, value float64) string {
	source := strconv.FormatFloat(value, 'g', -1, 64)
	switch strings.ToLower(descriptor.Unit) {
	case "", "unit", "ratio", "semitone", "cent":
		return source
	case "hz":
		return source + "Hz"
	case "ms":
		return source + "ms"
	case "db":
		return source + "dB"
	default:
		panic("unsupported scene registry unit: " + descriptor.Unit)
	}
}
