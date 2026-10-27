package project

import (
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
	"m31labs.dev/cicada/notation"
)

const modeledBandKit = "kit band_kit { bd = model.kick rs = model.cross_stick ch = model.hat_closed oh = model.hat_open cy = model.ride_bow }\n"
const modeledBandBeat = "pattern beat drums steps=16 { bd: x... .... x... .... rs: .... x... .... x... ch: x.x. x.x. x.x. x.x. }\n"

func requireModeledBand(t *testing.T, kit *[drum.LaneCount]engine.KitLaneBinding) {
	t.Helper()
	for _, want := range []struct {
		lane    drum.Lane
		profile modeledkit.Profile
	}{
		{drum.BD, modeledkit.Kick}, {drum.RS, modeledkit.CrossStick}, {drum.CH, modeledkit.HatClosed},
		{drum.OH, modeledkit.HatOpen}, {drum.CY, modeledkit.RideBow},
	} {
		if kit == nil || kit[want.lane].Kind != engine.KitLaneModeled || kit[want.lane].Model != want.profile {
			t.Fatalf("lane %s is not the modeled %s: %+v", drum.Names[want.lane], modeledkit.Names[want.profile], kit)
		}
	}
}

func TestModeledKitInAProjectWithImports(t *testing.T) {
	root, _ := libraryFixture(t)
	libraryWrite(t, root, "main.cicada", "import \"std/fx\"\n"+modeledBandKit+modeledBandBeat+"track drums band_kit { level = -12dB bd_tune = 0.9 send fx.reverb = 0.2 }\nscene main { drums = beat }\nsong { main }\n")
	pinLibraryFixture(t, root)
	score, ds, err := LoadScore(filepath.Join(root, "main.cicada"), nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("modeled kit beside an import: %v %+v", err, ds)
	}
	p, ds := FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("compile: %+v", ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	requireModeledBand(t, cfg.Track[0].Kit)
	if kick := cfg.Track[0].Kit[drum.BD]; kick.ModelParams.Tune != .9 || kick.ModelParams.Decay != 1 {
		t.Fatalf("track control did not reach the kick: %+v", kick)
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatalf("modeled configuration cannot load: %v", err)
	}
}

func TestLibraryDefinesModeledKit(t *testing.T) {
	for _, test := range []struct{ name, imports string }{
		{"no imports", ""},
		{"with an import", "import \"demo/phrases\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := libraryFixture(t)
			installLibrary(t, filepath.Join(root, "lib"), "demo/phrases", "phrase hook { 1 . 5 . }\n")
			libraryWrite(t, root, "lib/demo/tone/tone.cicada", test.imports+libraryVoice+"kit band_kit { bd = model.kick sd = glass rs = model.cross_stick ch = model.hat_closed oh = model.hat_open cy = model.ride_bow cp = builtin.cp }\n"+modeledBandBeat)
			libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack drums tone.band_kit { bd_tune = 0.9 ch_position = 0.2 }\nscene main { drums = tone.beat }\nsong { main }\n")
			score, ds := pinLibraryFixture(t, root).Parse()
			if score == nil || len(ds) != 0 {
				t.Fatalf("library kit: %+v", ds)
			}
			kit := score.Kits[0]
			if kit.Name != "demo.tone.band_kit" || kit.Bindings[0].Target != "model.kick" || kit.Bindings[1].Target != "demo.tone.glass" || score.Origins[kit.Name].Library != "demo/tone" {
				t.Fatalf("names/provenance: %+v", score.Kits)
			}
			p, ds := FromScore(score)
			if p == nil || len(ds) != 0 {
				t.Fatalf("compile: %+v", ds)
			}
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			lanes := cfg.Track[0].Kit
			requireModeledBand(t, lanes)
			if lanes[drum.SD].Kind != engine.KitLaneGraph || lanes[drum.CP].Kind != engine.KitLaneBuiltin {
				t.Fatalf("one library kit must mix modeled, instrument and built-in lanes: %+v", lanes)
			}
			if lanes[drum.BD].ModelParams.Tune != .9 || lanes[drum.CH].ModelParams.Position != .2 {
				t.Fatalf("track controls did not reach the library kit: %+v %+v", lanes[drum.BD], lanes[drum.CH])
			}
			if _, err := engine.New(cfg); err != nil {
				t.Fatalf("modeled configuration cannot load: %v", err)
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			q, err := DecodeJSON(data)
			if err != nil || !SemanticEqual(p, q) {
				t.Fatalf("semantic round trip: %v", err)
			}
			source, err := ToSource(q)
			if err != nil {
				t.Fatal(err)
			}
			flat, ds := notation.Parse(source)
			if flat == nil || len(ds) != 0 {
				t.Fatalf("flat source: %+v\n%s", ds, source)
			}
			r, ds := FromScore(flat)
			if r == nil || len(ds) != 0 || !SemanticEqual(p, r) {
				t.Fatalf("flat project: %+v", ds)
			}
		})
	}
}
