package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestPatternPreviewUsesCompatibleTracksAndRetainsTheChosenSound(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntrack low acid {}\ntrack keys piano {}\ntrack drums drums {}\ntrack vox audio {}\npattern idea notes steps=4 { 1 . 3 . }\npattern harmony notes steps=4 { [1 3 5] - - - }\npattern beat drums steps=4 { bd:x... }\npattern take performance length=3840ticks { note 60 at=17ticks length=703ticks velocity=82 }\nscene main { keys=idea drums=beat }\nsong { main }\n"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", Valid: true, Project: p})
	for _, test := range []struct{ pattern, track, present, absent string }{
		{"idea", "keys", `value="keys" selected`, `value="drums"`},
		{"beat", "drums", `value="drums" selected`, `value="keys"`},
		{"take", "keys", `value="keys" selected`, `value="drums"`},
		{"harmony", "keys", `value="keys" selected`, `value="low"`},
	} {
		page := getPage(t, client, address+"/?panel=patterns&pattern="+test.pattern+"&track="+test.track)
		start := strings.Index(page, `id="cicada-pattern-preview"`)
		if start < 0 {
			t.Fatal("missing pattern preview", test.pattern)
		}
		preview := page[start:]
		end := strings.Index(preview, `</select>`)
		if end < 0 {
			t.Fatal("missing preview sound selector")
		}
		if !strings.Contains(preview[:end], test.present) || strings.Contains(preview[:end], test.absent) || strings.Contains(preview[:end], `value="vox"`) || !strings.Contains(preview, `aria-label="Loop `+test.pattern+`"`) {
			t.Fatalf("incorrect sound choices for %s", test.pattern)
		}
	}
}
