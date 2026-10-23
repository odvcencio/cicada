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

func TestSecondarySplitUsesWorkingEntryAndRetainsProjectIdentity(t *testing.T) {
	for _, chance := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserved", true: "changed-working-pattern"}[chance], func(t *testing.T) {
			entry := "cicada 2\ntrack bass acid {}\npattern beat { " + plainSteps + " }\n"
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
