package project

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
	"m31labs.dev/cicada/notation"
)

const modeledKitSource = `cicada 1
tempo 120
seed 17
kit full {
 bd=model.kick; sd=model.snare; rs=model.rimshot; cp=model.cross_stick;
 lt=model.tom_low; mt=model.tom_mid; ht=model.tom_high;
 ch=model.hat_closed; oh=model.hat_open; cb=model.ride_bow; cy=model.crash;
}
kit details {
 ch=model.hat_pedal; oh=model.hat_half_open; cb=model.ride_bell; cy=model.splash;
}
track shells full {
 bd_tune=0.8 bd_decay=1.3 bd_position=0.6 bd_humanize=0.02
 bd_level=-12db bd_pan=-0.4
}
track cymbals details { ch_position=0.2 cb_pan=0.3 }
pattern beat drums steps=4 {
 bd:X...; sd:.x5..; rs:..x.; cp:...x;
 lt:x...; mt:.x..; ht:..x.; ch:...x; oh:x...; cb:.x..; cy:..x.;
}
scene main { shells=beat cymbals=beat }
song { main }
`

func TestModeledKitSourceJSONRoundTripAndBindings(t *testing.T) {
	parse := func(source []byte) *Project {
		t.Helper()
		score, diagnostics := notation.Parse(source)
		if score == nil || hasErrors(diagnostics) {
			t.Fatalf("parse: %+v", diagnostics)
		}
		p, diagnostics := FromScore(score)
		if p == nil || hasErrors(diagnostics) {
			t.Fatalf("compile: %+v", diagnostics)
		}
		return p
	}
	p := parse([]byte(modeledKitSource))
	cfg, err := CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	profiles := make(map[modeledkit.Profile]bool)
	for i := range p.Tracks {
		for _, binding := range cfg.Track[i].Kit {
			if binding.Kind == engine.KitLaneModeled {
				profiles[binding.Model] = true
			}
		}
	}
	if len(profiles) != 15 {
		t.Fatalf("two kits expose %d modeled articulations, want 15", len(profiles))
	}
	kick := cfg.Track[0].Kit[drum.BD]
	if kick.ModelParams != (modeledkit.Params{Tune: .8, Decay: 1.3, Position: .6, Humanize: .02}) || kick.ModelLevelDB != -12 || kick.ModelPan != -.4 {
		t.Fatalf("controls did not reach the kick: %+v", kick)
	}
	snare := cfg.Track[0].Kit[drum.SD]
	if snare.ModelParams != modeledkit.DefaultParams() || snare.ModelLevelDB != -6 || snare.ModelPan != 0 {
		t.Fatalf("unexpected default controls: %+v", snare)
	}
	if cfg.Track[1].Kit[drum.BD].Kind != engine.KitLaneOff {
		t.Fatal("an omitted lane must remain off")
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatalf("modeled configuration cannot load: %v", err)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !SemanticEqual(p, parse(source)) {
		t.Fatal("source/JSON/source round trip changed the modeled kit")
	}
}

func TestModeledKitRejectsInvalidControlsAndRouting(t *testing.T) {
	kit := Kit{ID: "modelkit", Lanes: map[string]string{"bd": "model.kick", "ch": "builtin.ch"}}
	for _, parameter := range []struct{ name, literal string }{
		{"bd_tune", "0.49"}, {"bd_tune", "2.01"}, {"bd_tune", "60hz"},
		{"bd_decay", "0.24"}, {"bd_decay", "2.01"}, {"bd_decay", "250ms"},
		{"bd_position", "-0.01"}, {"bd_position", "1.01"},
		{"bd_humanize", "-0.01"}, {"bd_humanize", "0.101"},
		{"bd_level", "-61db"}, {"bd_level", "7db"}, {"bd_level", "0.5"},
		{"bd_pan", "-1.01"}, {"bd_pan", "1.01"}, {"bd_position", "on"},
		{"bd_tone", "0.5"}, {"sd_tune", "1"}, {"ch_decay", "1"},
		{"tune", "1"}, {"zz_tune", "1"},
	} {
		t.Run(parameter.name+"="+parameter.literal, func(t *testing.T) {
			value, err := projectValue(parameter.literal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := CompileKitTrack(kit, nil, map[string]Value{parameter.name: value}); err == nil {
				t.Fatal("accepted invalid modeled control")
			}
		})
	}
	for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := CompileKitTrack(kit, nil, map[string]Value{"bd_tune": {Unit: "unit", Number: &number}}); err == nil {
			t.Fatal("accepted nonfinite modeled control")
		}
	}
	kit.Lanes["bd"] = "model.unknown"
	if _, err := CompileKit(kit, nil); err == nil {
		t.Fatal("accepted an unknown modeled articulation")
	}
}

func TestModeledKitSourceControlDiagnosticsArePositioned(t *testing.T) {
	for _, edit := range []struct{ before, after string }{
		{"bd_tune=0.8", "bd_tune=0.2"},
		{"bd_decay=1.3", "bd_decay=300ms"},
		{"bd_position=0.6", "bd_position=1.2"},
		{"ch_position=0.2", "bd_position=0.2"},
		{"model.crash", "model.unknown"},
	} {
		t.Run(edit.after, func(t *testing.T) {
			source := strings.Replace(modeledKitSource, edit.before, edit.after, 1)
			score, ds := notation.Parse([]byte(source))
			if score == nil {
				t.Fatal("invalid semantic source must remain grammar-valid")
			}
			_, extra := FromScore(score)
			ds = append(ds, extra...)
			for _, d := range ds {
				if d.Severity == "error" && (d.Code == "CICADA-PARAM" || d.Code == "CICADA-REFERENCE") && d.Position.Line > 1 && d.Position.Column > 0 {
					return
				}
			}
			t.Fatalf("missing positioned control diagnostic: %+v", ds)
		})
	}
}

func TestModeledKitSceneControlsKeepSynthesisPrepared(t *testing.T) {
	for _, control := range []struct {
		setting string
		allowed bool
	}{
		{"shells.bd_tune=60Hz", false},
		{"shells.bd_decay=300ms", false},
		{"shells.bd_level=-9db", true},
		{"shells.bd_pan=0.3", true},
	} {
		t.Run(control.setting, func(t *testing.T) {
			source := strings.Replace(modeledKitSource, "shells=beat cymbals=beat", "shells=beat cymbals=beat "+control.setting, 1)
			score, ds := notation.Parse([]byte(source))
			if score == nil || hasErrors(ds) {
				t.Fatalf("source must parse: %+v", ds)
			}
			p, ds := FromScore(score)
			if control.allowed {
				if p == nil || hasErrors(ds) {
					t.Fatalf("modeled mixer scene control rejected: %+v", ds)
				}
				if _, err := CompileEngine(p, 48_000, 128); err != nil {
					t.Fatal(err)
				}
				return
			}
			for _, d := range ds {
				if d.Code == "CICADA-UNSUPPORTED" && d.Severity == "error" {
					return
				}
			}
			t.Fatalf("scene accepted unprepared modeled synthesis control: %+v", ds)
		})
	}
}
