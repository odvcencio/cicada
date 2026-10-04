package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

const studioLibraryScore = "cicada 2\ntempo 120\nkey c minor\ntrack lead acid { cutoff=900Hz pan=0.2 }\npattern melody { c3 . g3 . }\nscene main { lead=melody }\nsong { main }\n"

func libraryStudio(t *testing.T) (http.Handler, string) {
	t.Helper()
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	filename := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(filename, []byte(studioLibraryScore), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := studioHandler(filename)
	if err != nil {
		t.Fatal(err)
	}
	return h, filename
}

func TestStudioLibraryListFiltersAndLocations(t *testing.T) {
	h, filename := libraryStudio(t)
	for _, dir := range []string{filepath.Join(filepath.Dir(filename), "lib", "local"), filepath.Join(os.Getenv("CICADA_LIBRARY"), "personal")} {
		if err := project.NewLibrary(filepath.Base(dir), dir); err != nil {
			t.Fatal(err)
		}
	}
	r := studioCall(t, h, "/api/library", nil)
	var state struct {
		Items  []studioLibraryItem
		Errors []string
		Tracks []string
	}
	if err := json.Unmarshal(r.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	locations := map[string]bool{}
	for _, item := range state.Items {
		locations[item.Location] = true
		if strings.HasPrefix(item.Name, "_") {
			t.Fatal("private declaration listed")
		}
	}
	if r.Code != 200 || len(state.Errors) != 0 || len(locations) != 3 || len(state.Tracks) != 1 {
		t.Fatalf("list: %d %+v", r.Code, state)
	}
	r = studioCall(t, h, "/api/library?kind=preset&q=ACID", nil)
	if err := json.Unmarshal(r.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 3 {
		t.Fatalf("filter: %+v", state.Items)
	}
}

func TestStudioLibraryPreviewsUseKernelAndDoNotWrite(t *testing.T) {
	h, filename := libraryStudio(t)
	r := studioCall(t, h, "/api/library", nil)
	var state struct{ Items []studioLibraryItem }
	_ = json.Unmarshal(r.Body.Bytes(), &state)
	for _, item := range state.Items {
		t.Run(item.Path+"/"+item.Name, func(t *testing.T) {
			r := studioCall(t, h, "/api/library/preview", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Path: item.Path, Item: item.Name, Mode: "browser", Rate: 48000})
			if r.Code != 200 {
				t.Fatalf("preview: %d %s", r.Code, r.Body.String())
			}
			cfg, err := kernelimage.Decode(r.Body.Bytes(), 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.New(cfg); err != nil {
				t.Fatal(err)
			}
		})
	}
	current, _ := os.ReadFile(filename)
	if string(current) != studioLibraryScore {
		t.Fatal("preview changed the score")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filename), "cicada.sum")); !os.IsNotExist(err) {
		t.Fatal("preview wrote pins")
	}
}

func TestStudioLibraryInsertPinsAndUndoRedo(t *testing.T) {
	for _, item := range []studioLibraryItem{{Path: "std/synth", Name: "glassbass", Kind: "instrument"}, {Path: "std/fx", Name: "drive", Kind: "fx"}, {Path: "std/fx", Name: "delay", Kind: "fx"}, {Path: "std/fx", Name: "reverb", Kind: "fx"}, {Path: "std/fx", Name: "comp", Kind: "fx"}, {Path: "std/presets", Name: "acid-bite", Kind: "preset"}} {
		t.Run(item.Name, func(t *testing.T) {
			h, filename := libraryStudio(t)
			r := studioCall(t, h, "/api/library/insert", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Path: item.Path, Item: item.Name, Track: "lead"})
			if r.Code != 200 {
				t.Fatalf("insert: %d %s", r.Code, r.Body.String())
			}
			current, _ := os.ReadFile(filename)
			if bytes.Count(current, []byte("import ")) != 1 {
				t.Fatal("import not inserted once")
			}
			if _, err := compileStudioSource(filename, current); err != nil {
				t.Fatal(err)
			}
			if item.Kind == "instrument" || item.Kind == "preset" {
				hand := strings.Replace(studioLibraryScore, "track lead acid", "track lead "+libraryReference(item), 1) + "\nimport \"" + item.Path + "\"\n"
				studioLibraryPCMEqual(t, filename, current, []byte(hand))
			} else {
				route := "insert=fx.drive"
				if item.Name == "delay" || item.Name == "reverb" {
					route = "send fx." + item.Name + "=0.4"
				}
				if item.Name == "comp" {
					route = "out=music"
				}
				hand := strings.Replace(studioLibraryScore, "pan=0.2 }", "pan=0.2 "+route+" }", 1) + "\nimport \"std/fx\"\n"
				if item.Name == "comp" {
					hand += "bus music { insert=fx.comp }\n"
				}
				studioLibraryPCMEqual(t, filename, current, []byte(hand))
			}
			r = studioCall(t, h, "/api/undo", studioEdit{Revision: studioRevision(current)})
			if r.Code != 200 {
				t.Fatalf("undo: %d %s", r.Code, r.Body.String())
			}
			undone, _ := os.ReadFile(filename)
			if string(undone) != studioLibraryScore {
				t.Fatal("undo did not restore source")
			}
			r = studioCall(t, h, "/api/redo", studioEdit{Revision: studioRevision(undone)})
			if r.Code != 200 {
				t.Fatalf("redo: %d %s", r.Code, r.Body.String())
			}
			redone, _ := os.ReadFile(filename)
			if !bytes.Equal(redone, current) {
				t.Fatal("redo did not restore source")
			}
			r = studioCall(t, h, "/api/library/insert", studioEdit{Revision: studioRevision(current), Path: item.Path, Item: item.Name, Track: "extra"})
			if r.Code != 200 {
				t.Fatalf("new track: %d %s", r.Code, r.Body.String())
			}
			current, _ = os.ReadFile(filename)
			if bytes.Count(current, []byte("import ")) != 1 {
				t.Fatal("duplicate import")
			}
		})
	}
}

func TestStudioLibrarySavePresetValuesAndUndo(t *testing.T) {
	h, filename := libraryStudio(t)
	r := studioCall(t, h, "/api/library/save", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Track: "lead", Name: "bright"})
	if r.Code != 200 {
		t.Fatalf("save: %d %s", r.Code, r.Body.String())
	}
	current, _ := os.ReadFile(filename)
	if !bytes.Contains(current, []byte("preset bright")) || !bytes.Contains(current, []byte("cutoff = 900Hz")) || !bytes.Contains(current, []byte("pan = 0.2")) {
		t.Fatalf("values: %s", current)
	}
	if _, err := compileStudioSource(filename, current); err != nil {
		t.Fatal(err)
	}
	bound := bytes.Replace(current, []byte("track lead acid { cutoff=900Hz pan=0.2 }"), []byte("track lead bright {}"), 1)
	studioLibraryPCMEqual(t, filename, bound, []byte(studioLibraryScore))
	r = studioCall(t, h, "/api/undo", studioEdit{Revision: studioRevision(current)})
	if r.Code != 200 {
		t.Fatalf("undo: %d %s", r.Code, r.Body.String())
	}
	current, _ = os.ReadFile(filename)
	if string(current) != studioLibraryScore {
		t.Fatal("save undo did not restore score")
	}
}

func studioLibraryPCMEqual(t *testing.T, filename string, a, b []byte) {
	t.Helper()
	for _, rate := range []int{44100, 48000} {
		var wavs [2]bytes.Buffer
		for i, source := range [][]byte{a, b} {
			score, ds, err := parseScoreForPath(filename, source)
			if err != nil || hasDiagnosticErrors(ds) {
				t.Fatalf("source: %v %+v", err, ds)
			}
			if _, err := render.WAV(score, render.Options{SampleRate: rate, Bits: 32, Bars: 1}, &wavs[i]); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(wavs[0].Bytes(), wavs[1].Bytes()) {
			t.Fatalf("written source differs from hand-written PCM at %d Hz", rate)
		}
		t.Logf("METRIC studio-source-edit rate=%d handwritten_pcm=byte-identical", rate)
	}
}

func TestStudioLibrarySavedPresetDefaultsAndImportedValues(t *testing.T) {
	h, filename := libraryStudio(t)
	r := studioCall(t, h, "/api/library/insert", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Path: "std/presets", Item: "acid-round", Track: "lead"})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	current, _ := os.ReadFile(filename)
	r = studioCall(t, h, "/api/library/save", studioEdit{Revision: studioRevision(current), Track: "lead", Name: "round"})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	saved, _ := os.ReadFile(filename)
	if !bytes.Contains(saved, []byte("instrument = acid")) || !bytes.Contains(saved, []byte("decay = 520ms")) || !bytes.Contains(saved, []byte("cutoff = 900Hz")) {
		t.Fatalf("inherited values: %s", saved)
	}
	source := []byte(strings.Replace(studioLibraryScore, "cutoff=900Hz pan=0.2", "cutoff=0.54kHz pan=0 mute=off", 1))
	updated, err := studioSavedPresetSource(filename, source, "lead", "defaults")
	if err != nil {
		t.Fatal(err)
	}
	declaration := string(updated[len(source):])
	if strings.Contains(declaration, "pan =") || strings.Contains(declaration, "mute =") {
		t.Fatalf("default included: %s", declaration)
	}
}

func TestStudioLibraryPreviewCallbackDoesNotAllocate(t *testing.T) {
	_, filename := libraryStudio(t)
	for _, item := range []studioLibraryItem{{Path: "std/synth", Name: "glassbass", Kind: "instrument"}, {Path: "std/drums", Name: "steel", Kind: "kit"}, {Path: "std/presets", Name: "acid-round", Kind: "preset"}, {Path: "std/fx", Name: "drive", Kind: "fx"}} {
		p, _, err := studioLibraryPreviewProject(filename, item)
		if err != nil {
			t.Fatal(err)
		}
		for _, rate := range []int{44100, 48000} {
			score, err := compileLiveProjectAtRate(filename, p, rate)
			if err != nil {
				t.Fatal(err)
			}
			player, err := liveplay.New(score, rate)
			if err != nil {
				t.Fatal(err)
			}
			var left, right [128]float32
			output := [][]float32{left[:], right[:]}
			session := &backendStudioAudio{reader: player, pcm: make([]byte, 128*8)}
			session.playing.Store(true)
			allocs := testing.AllocsPerRun(1000, func() {
				if err := session.renderPeriod(nil, output); err != nil {
					panic(err)
				}
			})
			player.Close()
			if allocs != 0 {
				t.Fatalf("preview callback allocated %g", allocs)
			}
			t.Logf("METRIC studio-preview item=%s rate=%d native_host_callback_allocs=0", item.Name, rate)
		}
	}
}

func TestStudioLibraryRejectsStaleInvalidAndPrivateEdits(t *testing.T) {
	h, filename := libraryStudio(t)
	for _, test := range []struct {
		endpoint string
		edit     studioEdit
		code     int
	}{
		{"insert", studioEdit{Revision: "stale", Path: "std/synth", Item: "glass", Track: "lead"}, 409},
		{"insert", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Path: "../escape", Item: "glass", Track: "lead"}, 422},
		{"insert", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Path: "std/synth", Item: "_private", Track: "lead"}, 422},
		{"save", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Track: "lead", Name: "lead"}, 422},
		{"save", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Track: "missing", Name: "bright"}, 422},
	} {
		r := studioCall(t, h, "/api/library/"+test.endpoint, test.edit)
		if r.Code != test.code {
			t.Fatalf("%s: %d %s", test.endpoint, r.Code, r.Body.String())
		}
	}
	current, _ := os.ReadFile(filename)
	if string(current) != studioLibraryScore {
		t.Fatal("rejected edit changed source")
	}
}

func TestStudioLibraryNativePreviewStopsOnPlay(t *testing.T) {
	_, filename := libraryStudio(t)
	tpt := newStudioTransport(filename)
	tpt.audioNull = true
	t.Cleanup(tpt.stop)
	p, _, err := studioLibraryPreviewProject(filename, studioLibraryItem{Path: "std/synth", Name: "glassbass", Kind: "instrument"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tpt.startPreview(filename, p); err != nil {
		t.Fatal(err)
	}
	first := tpt.preview
	if first == nil || tpt.playing {
		t.Fatal("preview changed transport state")
	}
	if err := tpt.startPreview(filename, p); err != nil {
		t.Fatal(err)
	}
	if tpt.preview == first {
		t.Fatal("second preview did not replace the first")
	}
	if err := tpt.start(); err != nil {
		t.Fatal(err)
	}
	if tpt.preview != nil || !tpt.playing {
		t.Fatal("play did not stop preview")
	}
}

func TestStudioLibraryFXSourceSpan(t *testing.T) {
	source := studioLibraryImport([]byte(studioLibraryScore), "std/fx")
	updated, err := studioLibrarySetting(source, "track_decl", "lead", "insert", "fx.drive")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = notation.ParseTree(updated)
	if err != nil {
		t.Fatalf("%v\n%s", err, updated)
	}
}

func TestStudioLibrarySavesSamplerPresetTargetAndOverrides(t *testing.T) {
	_, filename := libraryStudio(t)
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	if err := os.WriteFile(filepath.Join(filepath.Dir(filename), "wave.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte(fmt.Sprintf(`cicada 2
asset wave "wave.wav" { sha256="%x" format=wav frames=480 rate=48000Hz channels=1 }
sampler hit { asset=wave root=c3 mode=oneshot voices=8 }
preset looped { instrument=hit mode=loop voices=4 }
track lead looped { voices=2 }
pattern melody { c3 . g3 . }
scene main { lead=melody }
song { main }
`, sha256.Sum256(wav)))
	updated, err := studioSavedPresetSource(filename, source, "lead", "saved")
	if err != nil {
		t.Fatal(err)
	}
	declaration := string(updated[len(source):])
	if !strings.Contains(declaration, "instrument = hit") || !strings.Contains(declaration, "voices = 2") || !strings.Contains(declaration, "mode = loop") {
		t.Fatalf("sampler snapshot: %s", declaration)
	}
	if _, err := compileStudioSource(filename, updated); err != nil {
		t.Fatal(err)
	}
}

func TestStudioLibrarySavesTransitivePresetWithDirectImport(t *testing.T) {
	_, filename := libraryStudio(t)
	root := filepath.Dir(filename)
	tones := filepath.Join(root, "lib", "tones")
	colors := filepath.Join(os.Getenv("CICADA_LIBRARY"), "colors")
	if err := project.NewLibrary("tones", tones); err != nil {
		t.Fatal(err)
	}
	if err := project.NewLibrary("colors", colors); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(colors, "main.cicada"), []byte("import \"tones\"\npreset soft { instrument=tones.tone level=-9dB }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte("cicada 2\nimport \"colors\"\ntrack lead colors.soft {}\npattern melody { c3 . g3 . }\nscene main { lead=melody }\nsong { main }\n")
	if err := os.WriteFile(filename, source, 0600); err != nil {
		t.Fatal(err)
	}
	_, pins, err := studioLibraryPins(filename, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pins.Path, pins.After, 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := studioSavedPresetSource(filename, source, "lead", "saved")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("import \"tones\"")) || !bytes.Contains(updated, []byte("instrument = tones.tone")) {
		t.Fatalf("transitive snapshot: %s", updated)
	}
	if _, err := compileStudioSource(filename, updated); err != nil {
		t.Fatal(err)
	}
	studioLibraryPCMEqual(t, filename, updated, source)
}

func TestStudioLibraryKitInsertHandwrittenPCM(t *testing.T) {
	_, filename := libraryStudio(t)
	source := []byte("cicada 2\ntempo 120\ntrack beat drums {}\npattern rhythm drums { bd: X...x...X...x... ch: x.x.x.x.x.x.x.x. }\nscene main { beat=rhythm }\nsong { main }\n")
	if err := os.WriteFile(filename, source, 0600); err != nil {
		t.Fatal(err)
	}
	h, err := studioHandler(filename)
	if err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, h, "/api/library/insert", studioEdit{Revision: studioRevision(source), Path: "std/drums", Item: "steel", Track: "beat"})
	if r.Code != 200 {
		t.Fatalf("kit insert: %d %s", r.Code, r.Body.String())
	}
	current, _ := os.ReadFile(filename)
	hand := strings.Replace(string(source), "track beat drums", "track beat drums.steel", 1) + "\nimport \"std/drums\"\n"
	studioLibraryPCMEqual(t, filename, current, []byte(hand))
}

func TestStudioLibrarySavedPresetPreservesAudibleDefaultOverrides(t *testing.T) {
	_, filename := libraryStudio(t)
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	if err := os.WriteFile(filepath.Join(filepath.Dir(filename), "wave.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, declaration, target, settings string }{
		{"acid octave", "", "acid", "octave=3"},
		{"sampler settings", fmt.Sprintf("asset wave \"wave.wav\" { sha256=\"%x\" format=wav frames=480 rate=48000Hz channels=1 }\nsampler hit { asset=wave root=c3 mode=loop voices=8 }\n", sha256.Sum256(wav)), "hit", "root=c0 mode=oneshot voices=1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			track := "track lead " + test.target + " { " + test.settings + " }"
			declaration := test.declaration
			if test.target == "hit" {
				declaration += "preset original { instrument=hit " + test.settings + " }\n"
				track = "track lead original {}"
			}
			source := []byte("cicada 2\n" + declaration + track + "\npattern melody { 1 . 5 . }\nscene main { lead=melody }\nsong { main }\n")
			updated, err := studioSavedPresetSource(filename, source, "lead", "saved")
			if err != nil {
				t.Fatal(err)
			}
			bound := bytes.Replace(updated, []byte(track), []byte("track lead saved {}"), 1)
			if test.target == "hit" {
				for _, text := range [][]byte{source, bound} {
					p, err := compileStudioSource(filename, text)
					if err != nil {
						t.Fatal(err)
					}
					sampler := p.Samplers[len(p.Samplers)-1]
					if sampler.Mode != "oneshot" || sampler.Voices != 1 || sampler.RootMIDI != 12 {
						t.Fatalf("audible sampler settings changed: %+v", sampler)
					}
				}
			} else {
				studioLibraryPCMEqual(t, filename, source, bound)
			}
		})
	}
}
