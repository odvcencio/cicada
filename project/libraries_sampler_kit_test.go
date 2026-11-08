package project

import (
	"testing"
)

// A library kit can bind the library's own sample pack. The library qualifies
// the sampler name, so the binding splits at its last dot.
func TestLibraryKitBindsTheLibrarysSampler(t *testing.T) {
	root, _ := libraryFixture(t)
	libraryWrite(t, root, "lib/demo/tone/tone.cicada", "sampler shells { pack = \"packs/shells/manifest.json\" sha256 = \"0000000000000000000000000000000000000000000000000000000000000000\" root = c4 voices = 10 }\n"+
		"kit snares { sd = shells.d2 rs = shells.c#2 cp = shells.eb2 }\n"+
		"pattern beat drums steps=8 { sd: x... x... rs: ..x. ..x. }\n")
	libraryWrite(t, root, "main.cicada", "import \"demo/tone\"\ntrack drums tone.snares { sd_level = -3dB }\nscene main { drums = tone.beat }\nsong { main }\n")
	score, ds := pinLibraryFixture(t, root).Parse()
	if score == nil || len(ds) != 0 {
		t.Fatalf("library sampler kit: %+v", ds)
	}
	want := map[string]string{"sd": "demo.tone.shells.d2", "rs": "demo.tone.shells.c#2", "cp": "demo.tone.shells.eb2"}
	for _, binding := range score.Kits[0].Bindings {
		if want[binding.Lane] != binding.Target {
			t.Fatalf("lane %s binds %q, want %q", binding.Lane, binding.Target, want[binding.Lane])
		}
	}
	p, ds := FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("compile: %+v", ds)
	}
	lanes, err := KitSamplerLanes(p.Kits[0], nil, p.SamplerPackSet())
	if err != nil || len(lanes) != 3 {
		t.Fatalf("lanes %+v: %v", lanes, err)
	}
	for _, lane := range lanes {
		if lane.Sampler != "demo.tone.shells" {
			t.Fatalf("lane %s resolved to sampler %q", lane.Source, lane.Sampler)
		}
	}
}
