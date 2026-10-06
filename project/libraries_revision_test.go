package project

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportedEditionOneShorthandCompilesAcrossScoreEditions(t *testing.T) {
	for _, edition := range []int{1, 2} {
		t.Run(fmt.Sprint(edition), func(t *testing.T) {
			root, _ := libraryFixture(t)
			libraryWrite(t, root, "cicada.mod", fmt.Sprintf("project score\ncicada %d\nentry \"main.cicada\"\n", edition))
			libraryWrite(t, root, "lib/demo/tone/cicada.mod", "library demo/tone\ncicada 1\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n")
			libraryWrite(t, root, "lib/demo/tone/tone.cicada", libraryVoice+"fx delay { feedback=0.2 }\n")
			source := libraryScore
			if edition == 2 {
				source = strings.Replace(source, "tone.glass {}", "tone.glass { send tone.delay=-9dB }", 1)
			}
			libraryWrite(t, root, "main.cicada", source)
			pinLibraryFixture(t, root)
			score, ds, err := LoadScore(filepath.Join(root, "main.cicada"), nil)
			if err != nil || score == nil || hasErrors(ds) {
				t.Fatalf("edition-%d score rejected an edition-1 library: err=%v diagnostics=%+v", edition, err, ds)
			}
			p, ds := FromScore(score)
			if p == nil || hasErrors(ds) {
				t.Fatalf("edition-%d library effect compilation: %+v", edition, ds)
			}
			if len(p.Effects) != 1 || p.Effects[0].ID != "demo.tone.delay" || p.Effects[0].Kind != "delay" {
				t.Fatalf("namespaced effect lost its kind: %+v", p.Effects)
			}
			if _, err := CompileEngine(p, 48000, 128); err != nil {
				t.Fatalf("edition-%d library effect engine: %v", edition, err)
			}
		})
	}
}

func TestEditionTwoShorthandStillRejectedInLibraryAndScore(t *testing.T) {
	for _, location := range []string{"library", "score"} {
		t.Run(location, func(t *testing.T) {
			root, _ := libraryFixture(t)
			if location == "library" {
				libraryWrite(t, root, "lib/demo/tone/tone.cicada", libraryVoice+"fx delay { feedback=0.2 }\n")
			} else {
				libraryWrite(t, root, "main.cicada", libraryScore+"fx delay { feedback=0.2 }\n")
			}
			pinLibraryFixture(t, root)
			requireLibraryDiagnostic(t, root, "CICADA-VERSION")
		})
	}
}
