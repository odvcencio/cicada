package rendercheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

func TestSplitChecksTracksHiddenByMixer(t *testing.T) {
	for _, steps := range []string{plainSteps, chanceSteps} {
		for _, mixer := range []string{"audible", "muted", "solo", "master", "bus"} {
			t.Run(map[bool]string{true: "chance", false: "plain"}[steps == chanceSteps]+"/"+mixer, func(t *testing.T) {
				bass, drums, extra := "", "", ""
				if mixer == "muted" {
					bass = "mute = on"
				}
				if mixer == "solo" {
					drums = "solo = on"
				}
				if mixer == "master" {
					extra = "master { mute = on }\n"
				}
				if mixer == "bus" {
					bass = "out = music"
					extra = "bus music { mute = on }\n"
				}
				source := "cicada 2\ntrack bass acid { " + bass + " }\ntrack drums drums { " + drums + " }\n" + extra + "pattern beat { " + steps + " }\narrange { place run bass beat { at = @1.1.1 length = 4bars } }\n"
				path := writeProject(t, map[string]string{"main.cicada": source})
				result, err := edits.Apply([]byte(source), edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SplitClip{Entity: "placement:run", AtTick: 7680}}}, edits.Options{Compiler: projectCompiler{path}, Path: path, Edition: 2, RenderCheck: Cached(path, New(path))})
				if steps == chanceSteps {
					var refusal *edits.RefusalError
					if !errors.As(err, &refusal) || result != nil {
						t.Fatalf("expected refusal: result=%v err=%v", result, err)
					}
					if refusal.Cause != "splitting beat at bar 3 restarts its chance steps" {
						t.Fatalf("unexpected refusal: %q", refusal.Cause)
					}
				} else if err != nil || result == nil || !strings.Contains(string(result.Source), bass) || !strings.Contains(string(result.Source), drums) {
					t.Fatalf("split failed or changed mixer: result=%v err=%v", result, err)
				}
			})
		}
	}
}

func TestSecondarySplitUsesWorkingEntryAndRetainsProjectIdentity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		chance, muted bool
	}{{"preserved", false, false}, {"changed-working-pattern", true, false}, {"muted-track", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			chance := tc.chance
			mixer := ""
			if tc.muted {
				mixer = "mute = on"
			}
			entry := "cicada 2\ntrack bass acid {" + mixer + "}\npattern beat { " + plainSteps + " }\n"
			arrangement := "arrange { place run bass beat { at = @1.1.1 length = 4bars } }\n"
			path := writeProject(t, map[string]string{
				"cicada.mod":  "project scoped\ncicada 2\nentry \"main.cicada\"\nsource \"arrangement.cicada\"\nsource \"metadata.cicada\"\n",
				"main.cicada": entry, "arrangement.cicada": arrangement, "metadata.cicada": "title \"Original\"\n",
			})
			sources, err := project.ReadSources(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			options := edits.Options{Compiler: projectCompiler{path}, Path: path, Edition: 2, Sources: sources.Files, RenderCheck: Cached(path, New(path))}
			var intents []edits.Intent
			if chance {
				intents = append(intents, &edits.ReplaceText{Source: strings.Replace(entry, plainSteps, chanceSteps, 1)})
			}
			intents = append(intents, &edits.SplitClip{Entity: "placement:run", AtTick: 7680})
			result, err := edits.Apply([]byte(entry), edits.Envelope{Version: 1, Intents: intents}, options)
			if chance {
				var refusal *edits.RefusalError
				if !errors.As(err, &refusal) || result != nil || !strings.Contains(refusal.Cause, "restarts its chance steps") {
					t.Fatalf("working entry was not rendered: result=%v err=%v", result != nil, err)
				}
				return
			}
			if err != nil || result == nil {
				t.Fatal(err)
			}
			if string(result.Source) != entry || len(result.Files) != 1 || !result.RenderChecked || result.RenderIdentity == "" || result.RenderRecheck == nil {
				t.Fatal("split lost source ownership or render approval")
			}
			if current, ok := result.RenderRecheck(); !ok || current != result.RenderIdentity {
				t.Fatal("unchanged project lost its approval")
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "metadata.cicada"), []byte("title \"Another editor\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if current, ok := result.RenderRecheck(); ok && current == result.RenderIdentity {
				t.Fatal("secondary split approval survived another source changing")
			}
		})
	}
}

func TestSecondarySplitKeepsGoverningManifest(t *testing.T) {
	entry := "cicada 2\ntrack bass acid {}\npattern beat { " + plainSteps + " }\n"
	path := writeProject(t, map[string]string{
		"cicada.mod":  "project outer\ncicada 2\nentry \"main.cicada\"\nsource \"parts/arrangement.cicada\"\n",
		"main.cicada": entry,
	})
	parts := filepath.Join(filepath.Dir(path), "parts")
	if err := os.Mkdir(parts, 0700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"arrangement.cicada": "arrange { place run bass beat { at = @1.1.1 length = 4bars } }\n",
		// Resolving this nearer manifest would omit the actual project's sounds.
		"cicada.mod": "project nested\ncicada 2\nentry \"arrangement.cicada\"\n",
	} {
		if err := os.WriteFile(filepath.Join(parts, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := project.ReadSources(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := edits.Apply([]byte(entry), edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SplitClip{Entity: "placement:run", AtTick: 7680}}}, edits.Options{Compiler: projectCompiler{path}, Path: path, Edition: 2, Sources: sources.Files, RenderCheck: Cached(path, New(path))})
	if err != nil || result == nil || !result.RenderChecked || len(result.Files) != 1 {
		t.Fatalf("governing project was lost: result=%v err=%v", result != nil, err)
	}
}

func TestSplitChecksSwitchedOffDrumLane(t *testing.T) {
	source := "cicada 2\ntrack drums drums { bd_level = off }\npattern beat drums { bd: x?50.x?50.x?50.x?50.x?50.x?50.x?50.x?50. }\narrange { place run drums beat { at = @1.1.1 length = 4bars } }\n"
	path := writeProject(t, map[string]string{"main.cicada": source})
	result, err := edits.Apply([]byte(source), edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SplitClip{Entity: "placement:run", AtTick: 7680}}}, edits.Options{Compiler: projectCompiler{path}, Path: path, Edition: 2, RenderCheck: Cached(path, New(path))})
	var refusal *edits.RefusalError
	if !errors.As(err, &refusal) || result != nil || refusal.Cause != "splitting beat at bar 3 restarts its chance steps" {
		t.Fatalf("expected chance refusal: result=%v err=%v", result, err)
	}
}

func TestCachedSplitRefusesUnscopedInner(t *testing.T) {
	source := "cicada 2\ntrack bass acid {}\npattern beat { " + plainSteps + " }\narrange { place run bass beat { at = @1.1.1 length = 4bars } }\n"
	path := writeProject(t, map[string]string{"main.cicada": source})
	check := Cached(path, &countedCheck{inner: New(path)})
	_, err := edits.Apply([]byte(source), edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SplitClip{Entity: "placement:run", AtTick: 7680}}}, edits.Options{Compiler: projectCompiler{path}, Path: path, Edition: 2, RenderCheck: check})
	var refusal *edits.RefusalError
	if !errors.As(err, &refusal) || refusal.Cause != edits.ErrSplitRenderCheck.Error() {
		t.Fatalf("expected split scope refusal: %v", err)
	}
}
