package project

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const presetInstrument = `instrument glassbass {
 octave = 2
 param cutoff = 540Hz
 param bite = 0.58
 voice mono { out = lowpass(saw(pitch), cutoff) * bite * env(gate, 90ms) }
}
`
const presetSong = "\npattern melody { 1 . 3 . }\nscene verse { lead = melody }\nsong { verse }\n"

func TestPresetsLowerToTrackValues(t *testing.T) {
	for _, test := range []struct{ name, decl, target, settings string }{
		{"authored", presetInstrument, "glassbass", "cutoff=900Hz bite=0.7"},
		{"acid", "", "acid", "cutoff=900Hz decay=180ms wave=0.6"},
		{"drums", "", "drums", "bd_tune=52Hz sd_decay=170ms"},
	} {
		t.Run(test.name, func(t *testing.T) {
			song := presetSong
			if test.target == "drums" {
				song = "\npattern beat drums { bd: X... sd: ..x. }\nscene verse { lead=beat }\nsong { verse }\n"
			}
			preset, ds := notation.Parse([]byte(test.decl + "preset bright { instrument=" + test.target + " " + test.settings + " }\ntrack lead bright {}" + song))
			if preset == nil || hasErrors(ds) {
				t.Fatalf("parse: %+v", ds)
			}
			inline, ds := notation.Parse([]byte(test.decl + "track lead " + test.target + " { " + test.settings + " }" + song))
			if inline == nil || hasErrors(ds) {
				t.Fatalf("inline: %+v", ds)
			}
			a, ds := FromScore(preset)
			if a == nil {
				t.Fatalf("preset: %+v", ds)
			}
			b, ds := FromScore(inline)
			if b == nil {
				t.Fatalf("inline: %+v", ds)
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatal("preset did not lower to the ordinary semantic project")
			}
			if preset.Tracks[0].Kind != "bright" {
				t.Fatal("source score mutated")
			}
		})
	}
}

func TestPresetLayers(t *testing.T) {
	score, ds := notation.Parse([]byte(presetInstrument + "preset glassbass.bright { instrument=glassbass cutoff=900Hz bite=0.7 }\ntrack lead glassbass.bright { bite=0.8 level=-9dB }\npattern melody { 1 . }\nscene verse { lead=melody lead.level=-6dB }\nsong { verse }"))
	if hasErrors(ds) {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	if *p.Tracks[0].Params["cutoff"].Number != 900 || *p.Tracks[0].Params["bite"].Number != .8 || p.Tracks[0].Mixer.GainDB != -9 || *p.Scenes[0].Settings[0].Value.Number != -6 {
		t.Fatalf("layers: %+v", p)
	}
}

func TestPresetOverridesRejectDuplicateRouting(t *testing.T) {
	for _, settings := range []string{
		"send echo=0.2 send echo=0.8",
		"out=music out=sfx",
		"insert=echo insert=none",
		"send_a=0.2 send_a=0.8",
		"send_pre=true send_pre=false",
	} {
		t.Run(settings, func(t *testing.T) {
			_, ds := notation.Parse([]byte("cicada 2\nfx echo delay {}\npreset bright { instrument=acid }\ntrack lead bright { " + settings + " }" + presetSong))
			for _, d := range ds {
				if d.Code == "CICADA-DUPLICATE" {
					return
				}
			}
			t.Fatalf("duplicate routing accepted: %+v", ds)
		})
	}
	_, ds := notation.Parse([]byte("cicada 2\nfx echo delay {}\nfx room reverb {}\npreset bright { instrument=acid }\ntrack lead bright { send echo=0.2 send room=0.8 }" + presetSong))
	if hasErrors(ds) {
		t.Fatalf("distinct sends rejected: %+v", ds)
	}
}

func TestPresetOverridesRejectDuplicateSidechains(t *testing.T) {
	_, ds := notation.Parse([]byte("cicada 2\npreset squeeze { instrument=builtin.comp }\nfx duck squeeze { sidechain=lead sidechain=music }\nbus music { insert=duck }\ntrack lead acid {}" + presetSong))
	for _, d := range ds {
		if d.Code == "CICADA-DUPLICATE" {
			return
		}
	}
	t.Fatalf("duplicate sidechains accepted: %+v", ds)
}

func TestDottedLibraryPresets(t *testing.T) {
	root, _ := libraryFixture(t)
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", libraryVoice+"preset glass.bright { instrument=glass level=-9dB }\npreset _glass.bright { instrument=glass }\n")
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack lead tone.glass.bright {}\nscene verse { lead=tone.melody }\nsong { verse }\n")
	pinLibraryFixture(t, root)
	score, ds, err := LoadScore(filepath.Join(root, "main.cicada"), nil)
	if err != nil || score == nil || hasErrors(ds) {
		t.Fatalf("dotted imported preset: %v %+v", err, ds)
	}
	p, ds := FromScore(score)
	if p == nil || p.Tracks[0].Kind != "demo.tone.glass" || p.Tracks[0].Mixer.GainDB != -9 {
		t.Fatalf("dotted preset lowering: %+v %+v", p, ds)
	}
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack lead tone._glass.bright {}\nscene verse { lead=tone.melody }\nsong { verse }\n")
	requireLibraryDiagnostic(t, root, "CICADA-LIB-PRIVATE")
}

func TestPresetDiagnosticsHaveFileAndValuePosition(t *testing.T) {
	for _, test := range []struct{ body, code string }{
		{"instrument=acid mystery=2", "CICADA-PRESET-PARAM"},
		{"instrument=acid cutoff=20ms", "CICADA-PRESET-UNIT"},
		{"instrument=acid cutoff=30000Hz", "CICADA-PRESET-RANGE"},
		{"instrument=acid cutoff=hello", "CICADA-PRESET-TYPE"},
		{"instrument=missing cutoff=900Hz", "CICADA-PRESET-TARGET"},
		{"instrument=acid instrument=drums", "CICADA-DUPLICATE"},
		{"instrument=other", "CICADA-PRESET-TARGET"},
		{"instrument=acid insert=none", "CICADA-PRESET-PARAM"},
	} {
		t.Run(test.body, func(t *testing.T) {
			_, ds := notation.ParseSource(notation.SourceFile{Path: "values.cicada", Source: []byte("preset bright {\n " + test.body + "\n}\ntrack lead bright {}" + presetSong)})
			for _, d := range ds {
				if d.Code == test.code {
					if d.Position.File != "values.cicada" || d.Position.Line != 2 || d.Position.Column < 2 {
						t.Fatalf("location: %+v", d)
					}
					return
				}
			}
			t.Fatalf("missing %s: %+v", test.code, ds)
		})
	}
}

func TestPresetWrongKindAndOverrideDiagnostics(t *testing.T) {
	for _, src := range []string{
		"fx grit drive {}\npreset dirty { instrument=grit mix=0.7 }\ntrack lead dirty {}",
		"preset bright { instrument=acid cutoff=900Hz }\nfx grit bright {}\ntrack lead acid {}",
	} {
		_, ds := notation.Parse([]byte(src + presetSong))
		found := false
		for _, d := range ds {
			found = found || d.Code == "CICADA-PRESET-TARGET"
		}
		if !found {
			t.Fatalf("wrong target accepted: %+v", ds)
		}
	}
	_, ds := notation.Parse([]byte("preset bright { instrument=acid cutoff=900Hz }\ntrack lead bright { cutoff=1ms }" + presetSong))
	found := false
	for _, d := range ds {
		found = found || d.Code == "CICADA-PRESET-UNIT"
	}
	if !found {
		t.Fatalf("bad override: %+v", ds)
	}
}

func TestEffectPresetAndAuthoredKitLane(t *testing.T) {
	source := presetInstrument + `preset soft { instrument=glassbass bite=0.3 }
kit custom { bd=soft }
preset dirty { instrument=builtin.drive mix=0.7 }
fx grit dirty { mix=0.8 }
track lead custom { insert=grit }
pattern beat drums { bd: X... }
scene verse { lead=beat }
song { verse }
`
	score, ds := notation.Parse([]byte(source))
	if hasErrors(ds) {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	if *p.Effects[0].Params["mix"].Number != .8 {
		t.Fatal("effect override lost")
	}
	if _, err := CompileEngine(p, 48000, 128); err != nil {
		t.Fatal(err)
	}
}

func TestPresetSamplerSettingsRetainAsset(t *testing.T) {
	source := `cicada 2
asset wave "wave.wav" { sha256="` + strings.Repeat("0", 64) + `" frames=480 rate=48000Hz channels=1 format=wav }
sampler hit { asset=wave root=c3 mode=oneshot voices=8 }
preset looped { instrument=hit mode=loop voices=4 root=c4 }
track lead looped { voices=2 level=-6dB }
pattern melody { 1 . }
scene verse { lead=melody }
song { verse }
`
	score, ds := notation.Parse([]byte(source))
	if hasErrors(ds) {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	got := p.Samplers[len(p.Samplers)-1]
	if got.Asset != "wave" || got.Voices != 2 || got.Mode != "loop" || got.RootMIDI != 60 {
		t.Fatalf("sampler: %+v", got)
	}
}

func TestPresetLibraryPinsPrivacyAndLocations(t *testing.T) {
	root, _ := libraryFixture(t)
	source := libraryVoice + "preset bright { instrument=glass level=-9dB }\npreset _secret { instrument=acid cutoff=900Hz }\n"
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", source)
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack lead tone.bright {}\nscene verse { lead=tone.melody }\nsong { verse }\n")
	pinLibraryFixture(t, root)
	score, ds, err := LoadScore(filepath.Join(root, "main.cicada"), nil)
	if err != nil || score == nil || hasErrors(ds) {
		t.Fatalf("imported preset: %+v %v", ds, err)
	}
	p, ds := FromScore(score)
	if p == nil || p.Tracks[0].Kind != "demo.tone.glass" || p.Tracks[0].Mixer.GainDB != -9 {
		t.Fatalf("compiled preset: %+v %+v", p, ds)
	}
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack lead tone._secret {}"+presetSong)
	requireLibraryDiagnostic(t, root, "CICADA-LIB-PRIVATE")
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack lead tone.bright {}\nscene verse { lead=tone.melody }\nsong { verse }\n")
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", strings.ReplaceAll(source, "-9dB", "-6dB"))
	requireLibraryDiagnostic(t, root, "CICADA-LIB-HASH")
	bad := libraryVoice + "preset bright { instrument=acid cutoff=10ms }\n"
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", bad)
	pinLibraryFixture(t, root)
	d := requireLibraryDiagnostic(t, root, "CICADA-PRESET-UNIT")
	if d.Position.File != filepath.Join(root, "lib/demo/tone/tone.cicada") || d.Position.Line != 4 || d.Position.Column < 30 {
		t.Fatalf("library diagnostic: %+v", d)
	}
}

func TestPresetsAcrossSourceFiles(t *testing.T) {
	score, ds := notation.ParseFiles([]notation.SourceFile{
		{Path: "main.cicada", Source: []byte("track lead bright {}" + presetSong)},
		{Path: "sounds.cicada", Source: []byte("preset bright { instrument=glassbass cutoff=900Hz }\n" + presetInstrument)},
	}, 2)
	if score == nil || hasErrors(ds) {
		t.Fatalf("cross-file preset: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || *p.Tracks[0].Params["cutoff"].Number != 900 {
		t.Fatalf("compile: %+v", ds)
	}
	_, ds = notation.ParseFiles([]notation.SourceFile{
		{Path: "main.cicada", Source: []byte("preset bright { instrument=acid }\ntrack lead bright {}" + presetSong)},
		{Path: "sounds.cicada", Source: []byte("preset bright { instrument=acid }\n")},
	}, 2)
	for _, d := range ds {
		if d.Code == "CICADA-DUPLICATE" && d.Position.File == "sounds.cicada" && d.Related.File == "main.cicada" {
			return
		}
	}
	t.Fatalf("duplicate locations: %+v", ds)
}

func TestEffectPresetUsesDeclaredDefaultsWithoutExtraInstance(t *testing.T) {
	score, ds := notation.Parse([]byte("fx prototype delay { feedback=0.2 }\npreset long { instrument=prototype feedback=0.3 mix=0.7 time=1/8 pingpong=on }\nfx echo long { feedback=0.4 }\ntrack lead acid { send echo=-12dB }" + presetSong))
	if score == nil || hasErrors(ds) {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	if len(p.Effects) != 1 || p.Effects[0].ID != "echo" || p.Effects[0].Kind != "delay" || *p.Effects[0].Params["feedback"].Number != .4 {
		t.Fatalf("effect preset: %+v", p.Effects)
	}
	if _, err := CompileEngine(p, 48000, 128); err != nil {
		t.Fatal(err)
	}
}

func TestEffectPresetRetainsLegacySendTargets(t *testing.T) {
	for _, test := range []struct{ kind, send string }{{"delay", "send_a"}, {"reverb", "send_b"}} {
		t.Run(test.kind, func(t *testing.T) {
			score, ds := notation.Parse([]byte("cicada 1\nfx " + test.kind + " { mix=0.2 }\npreset wet { instrument=" + test.kind + " mix=0.8 }\nfx echo wet {}\ntrack lead acid { " + test.send + "=0.5 }" + presetSong))
			if score == nil {
				t.Fatalf("parse: %+v", ds)
			}
			resolved, ds := notation.ResolvePresets(score)
			if hasErrors(ds) || len(resolved.Effects) != 2 || resolved.Effects[0].Name != test.kind {
				t.Fatalf("legacy target pruned: %+v %+v", resolved.Effects, ds)
			}
			p, ds := FromScore(score)
			if p != nil || !strings.Contains(fmt.Sprint(ds), "multiple "+test.kind+" instances") {
				t.Fatalf("expected existing instance limit: %+v %+v", p, ds)
			}
		})
	}
}
