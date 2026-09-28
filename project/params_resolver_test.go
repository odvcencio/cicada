package project

import (
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const parameterPathScore = "fx delay { feedback = 0.2 }\ntrack bass acid { send_a = 0.2 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"

func TestResolveParameterPathOwnerAndKernelAddress(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(parameterPathScore))
	if score == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("parse base score: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("compile base project: %+v", diagnostics)
	}
	for _, test := range []struct {
		path, kind, owner, id string
		track                 uint8
	}{
		{"bass.send.delay", "track", "bass", "mix.send_a", 0},
		{"delay.feedback", "effect", "delay", "fx.delay.feedback", 0xff},
		{"tempo", "global", "tempo", "global.tempo", 0xff},
	} {
		resolved, err := ResolveParameterPath(p, test.path)
		if err != nil {
			t.Fatalf("resolve %s: %v", test.path, err)
		}
		if resolved.Path != test.path || resolved.OwnerKind != test.kind || resolved.Owner != test.owner || resolved.Descriptor.ID != test.id || resolved.ID == 0 || resolved.Track != test.track {
			t.Errorf("resolved %s = %+v", test.path, resolved)
		}
	}
	for path, code := range map[string]string{
		"ghost.cutoff": "CICADA-REFERENCE",
		"bass.unknown": "CICADA-PARAM",
		"master.level": "CICADA-UNSUPPORTED",
	} {
		_, err := ResolveParameterPath(p, path)
		var pathErr *PathError
		if !errors.As(err, &pathErr) || pathErr.Code != code {
			t.Errorf("resolve %s error = %v, want %s", path, err, code)
		}
	}
}

func TestResolveParameterPathForCustomVoiceAndKitTracks(t *testing.T) {
	source := "instrument kick { voice mono { out = sine(pitch) * env(gate, 120ms); } }\nkit steel { bd = kick; ch = builtin.ch; }\ntrack keys kick {}\ntrack drums steel {}\npattern beat drums steps=1 { bd: x; }\nscene main { drums=beat keys.pan=0.25 drums.bd_tune=50Hz }\nsong { main }\n"
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("parse custom voice/kit score: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("compile custom voice/kit score: %+v", diagnostics)
	}
	for _, path := range []string{"keys.pan", "drums.bd_tune"} {
		if _, err := ResolveParameterPath(p, path); err != nil {
			t.Errorf("resolve %s: %v", path, err)
		}
	}
}

func TestScenePathValidationCodesAndAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name, source, path, code string
	}{
		{"unknown owner", parameterPathScore + "scene extra { ghost.cutoff=900Hz }\n", "ghost.cutoff", "CICADA-REFERENCE"},
		{"unknown setting", parameterPathScore + "scene extra { bass.mystery=1 }\n", "bass.mystery", "CICADA-PARAM"},
		{"bad unit", parameterPathScore + "scene extra { bass.cutoff=3dB }\n", "bass.cutoff", "CICADA-UNIT"},
		{"unknown toggle value", parameterPathScore + "scene extra { delay.pingpong=yes }\n", "delay.pingpong", "CICADA-UNIT"},
		{"invalid toggle spelling", parameterPathScore + "scene extra { delay.pingpong=maybe }\n", "delay.pingpong", "CICADA-UNIT"},
		{"nonlive", "fx drive { shape = soft }\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene main { bass=riff drive.shape=hard }\nsong { main }\n", "drive.shape", "CICADA-UNSUPPORTED"},
		{"ambiguous owner", "fx delay { feedback = 0.2 }\ntrack delay acid {}\npattern riff acid steps=1 { 1 }\nscene main { delay=riff delay.feedback=0.3 }\nsong { main }\n", "delay.feedback", "CICADA-REFERENCE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := notation.Parse([]byte(test.source))
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == test.code && (test.name != "ambiguous owner" || strings.Contains(diagnostic.Message, "hint:")) {
					return
				}
			}
			t.Fatalf("path %s did not report %s: %+v", test.path, test.code, diagnostics)
		})
	}
}
