package edit_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestPrintsShareSecondaryArrangementAndKeepEntryMetadata(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n"} {
		entry := []byte(strings.ReplaceAll("cicada 2\ntempo 120\ntrack bass acid {}\ntrack vox audio {}\npattern pulse { c3 . g3 . }\n", "\n", nl))
		part := []byte(strings.ReplaceAll("// retain arrangement comments\narrange { place backing bass pulse { at = @1.1.1 length = 4bars } }\n", "\n", nl))
		files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "arrangement.cicada", Source: part}}
		compiler := multiFileCompiler{path: "main.cicada", files: files}
		asset := edits.PrintAsset{Path: "printed.wav", SHA256: strings.Repeat("ab", 32), Frames: 48000, RateHz: 48000}
		comp := edits.VocalComp{Track: "vox", Tempo: 120000, Asset: "comp-a", Segments: []edits.VocalSegment{{Take: "take-a", Start: 0, End: 3840}}}
		result, err := edits.Apply(entry, edits.Envelope{Version: 1, Intents: []edits.Intent{
			&edits.PlaceSamplerPrint{Name: "sampled", Track: "pads", At: "@3.1.1", Asset: asset},
			&edits.PlaceVocalComp{Comp: comp, Asset: asset},
		}}, edits.Options{Path: compiler.path, Edition: 2, Compiler: compiler, Sources: files, ParseProject: compiler.parse})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Files) != 1 || result.Files[0].Path != "arrangement.cicada" || !bytes.Equal(result.Files[0].Before, part) {
			t.Fatal("prints lost their original owning-source snapshot")
		}
		for _, want := range []string{"track pads audio {}", "asset sampled-audio", "asset comp-a", edits.VocalCompPrefix} {
			if !bytes.Contains(result.Source, []byte(want)) {
				t.Fatalf("entry lost %q", want)
			}
		}
		if bytes.Contains(result.Source, []byte("arrange {")) {
			t.Fatal("print duplicated the arrangement in the entry")
		}
		after := result.Files[0].After
		for _, want := range []string{"// retain arrangement comments", "place backing", "place sampled pads sampled-clip", "place vocal-vox vox comp-a-clip"} {
			if !bytes.Contains(after, []byte(want)) {
				t.Fatalf("arrangement lost %q: %s", want, after)
			}
		}
		if nl == "\r\n" {
			for _, source := range [][]byte{result.Source, after} {
				if bytes.Contains(bytes.ReplaceAll(source, []byte(nl), nil), []byte("\n")) {
					t.Fatal("print changed line endings")
				}
			}
		}
		// Recomping must replace entry metadata and the owned placement together,
		// while retaining the sampler print and unrelated declarations.
		if nl == "\r\n" {
			start := bytes.Index(result.Source, []byte("// cicada-vocal-begin"))
			result.Source = append(bytes.Clone(result.Source[:start]), bytes.ReplaceAll(result.Source[start:], []byte("\r\n"), []byte("\n"))...)
		}
		files[0].Source, files[1].Source = result.Source, after
		compiler.files = files
		comp.Asset = "comp-b"
		comp.Segments[0].GainDB = -3
		updated, err := edits.Apply(result.Source, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.PlaceVocalComp{Comp: comp, Previous: &edits.VocalPrevious{Asset: "comp-a"}, Asset: asset}}}, edits.Options{Path: compiler.path, Edition: 2, Compiler: compiler, Sources: files, ParseProject: compiler.parse})
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(updated.Source, []byte("asset comp-a")) || !bytes.Contains(updated.Source, []byte("asset comp-b")) || bytes.Count(updated.Source, []byte(edits.VocalCompPrefix)) != 1 {
			t.Fatal("recomp duplicated or lost metadata")
		}
		if len(updated.Files) != 1 || !bytes.Contains(updated.Files[0].After, []byte("place sampled pads sampled-clip")) || !bytes.Contains(updated.Files[0].After, []byte("place vocal-vox vox comp-b-clip")) {
			t.Fatal("recomp changed another print or lost its placement")
		}
	}
}

func TestAutomationEditsExistingLaneInItsOwningSource(t *testing.T) {
	entry := []byte("cicada 2\ntrack vox audio {}\n")
	part := []byte("track bass acid {}\npattern pulse { c3 . }\narrange { place backing bass pulse { at = @1.1.1 length = 4bars } }\nautomate vox.level { @1.1.1 -6dB linear }\n")
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "arrangement.cicada", Source: part}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	result, err := edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.SetAutomationPoint{Entity: "param:vox.level", At: "@2.1.1", Value: "-3dB"}}}, edits.Options{Path: compiler.path, Edition: 2, Compiler: compiler, Sources: files, ParseProject: compiler.parse})
	if err != nil || result == nil || !bytes.Equal(result.Source, entry) || len(result.Files) != 1 || result.Files[0].Path != "arrangement.cicada" || !bytes.Equal(result.Files[0].Before, part) || bytes.Count(result.Files[0].After, []byte("automate vox.level")) != 1 || !bytes.Contains(result.Files[0].After, []byte("@2.1.1 -3dB")) {
		t.Fatalf("automation must update the owning lane without duplication: %v %v", result, err)
	}
}

func TestVocalPrintConvertsOwningSongSource(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("newline=%q", nl), func(t *testing.T) {
			entry := []byte("cicada 2\ntempo 120\ntrack bass acid {}\ntrack vox audio {}\npattern pulse { c3 . g3 . }\n")
			scenes := []byte("scene first { bass=pulse vox=off }\nscene held { bass=keep }\nscene quiet { bass=off }\n")
			song := []byte(strings.ReplaceAll("// keep song notes\nsong { first*2 // keep phrase note\nheld quiet first } // keep end comment\n", "\n", nl))
			files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "scenes.cicada", Source: scenes}, {Path: "song.cicada", Source: song}}
			compiler := multiFileCompiler{path: "main.cicada", files: files}
			comp := edits.VocalComp{Track: "vox", Tempo: 120000, Asset: "comp", Segments: []edits.VocalSegment{{Take: "take-a", Start: 0, End: 5 * 3840}}}
			asset := edits.PrintAsset{Path: "printed.wav", SHA256: strings.Repeat("ab", 32), Frames: 480000, RateHz: 48000}
			result, err := edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.PlaceVocalComp{Comp: comp, Asset: asset}}}, edits.Options{Path: compiler.path, Edition: 2, Compiler: compiler, Sources: files, ParseProject: compiler.parse})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(result.Source, entry) || bytes.Contains(result.Source, []byte("arrange {")) || len(result.Files) != 1 || result.Files[0].Path != "song.cicada" || !bytes.Equal(result.Files[0].Before, song) {
				t.Fatal("conversion moved definitions or lost original song bytes")
			}
			after := result.Files[0].After
			for _, want := range []string{"// keep song notes" + nl, "// keep phrase note" + nl, "// keep end comment" + nl, "place backing-1 bass pulse { at = 0ticks length = 11520ticks }", "place backing-2 bass pulse { at = 15360ticks length = 3840ticks }", "place vocal-vox"} {
				if !bytes.Contains(after, []byte(want)) {
					t.Fatalf("lost %q: %s", want, after)
				}
			}
			if nl == "\r\n" && bytes.Contains(bytes.ReplaceAll(after, []byte(nl), nil), []byte("\n")) {
				t.Fatal("conversion changed song line endings")
			}
			if !bytes.Equal(files[1].Source, scenes) || !bytes.Equal(files[2].Source, song) {
				t.Fatal("conversion mutated supplied source buffers")
			}
		})
	}
}

func TestVocalSongConversionRefusesUnsupportedSettings(t *testing.T) {
	for _, split := range []bool{false, true} {
		entry := []byte("cicada 2\ntempo 120\ntrack bass acid {}\ntrack vox audio {}\npattern pulse { c3 . }\n")
		part := []byte("scene first { bass=pulse vox=off bass.mute=true }\nsong { first*2 }\n")
		files := []notation.SourceFile{{Path: "main.cicada", Source: entry}, {Path: "song.cicada", Source: part}}
		if !split {
			entry = append(entry, part...)
			files = files[:1]
			files[0].Source = entry
		}
		compiler := multiFileCompiler{path: "main.cicada", files: files}
		in := &edits.PlaceVocalComp{Comp: edits.VocalComp{Track: "vox", Tempo: 120000, Asset: "comp", Segments: []edits.VocalSegment{{Take: "take-a", Start: 0, End: 3840}}}, Asset: edits.PrintAsset{Path: "printed.wav", SHA256: strings.Repeat("ab", 32), Frames: 48000, RateHz: 48000}}
		result, err := edits.Apply(entry, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{in}}, edits.Options{Path: compiler.path, Edition: 2, Compiler: compiler, Sources: files, ParseProject: compiler.parse})
		if err == nil || result != nil || !strings.Contains(err.Error(), "cannot") {
			t.Fatalf("scene settings must not disappear: %v %v", result, err)
		}
	}
}
