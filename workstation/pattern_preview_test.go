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
		selectStart := strings.Index(preview[:end], "<select")
		if selectStart < 0 {
			t.Fatal("missing preview sound selector")
		}
		selectEnd := strings.Index(preview[selectStart:end], ">")
		if selectEnd < 0 {
			t.Fatal("missing preview sound selector tag end")
		}
		selectTag := preview[selectStart : selectStart+selectEnd]
		if !strings.Contains(selectTag, `data-pattern-preview-track`) || !strings.Contains(selectTag, `data-studio-view-control`) {
			t.Fatal("preview sound selector must be a studio view control")
		}
		if !strings.Contains(preview[:end], test.present) || strings.Contains(preview[:end], test.absent) || strings.Contains(preview[:end], `value="vox"`) || !strings.Contains(preview, `aria-label="Loop `+test.pattern+`"`) {
			t.Fatalf("incorrect sound choices for %s", test.pattern)
		}
	}
}

func TestClipToolbarAndLegacyPatternLayout(t *testing.T) {
	source := "cicada 2\ntrack keys piano {}\npattern first notes { c4 . . . }\npattern second notes { e4 . . . }\nscene main { keys=first }\nsong { main }\n"
	score, ds := notation.Parse([]byte(source))
	p, more := project.FromScore(score)
	if len(ds) != 0 || len(more) != 0 {
		t.Fatal(ds, more)
	}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", Valid: true, Source: source, Project: p})
	for _, played := range []bool{false, true} {
		if played {
			p.Patterns[0].Performance = &project.Performance{LengthTicks: 960, Notes: []project.PerformedNote{{Note: 60, Tick: 0, EndTick: 240, Velocity: 90}}}
		}
		page := getPage(t, client, address+"/studio?dock=clip&pattern=first&region=detail")
		start := strings.Index(page, `class="clip-toolbar"`)
		if start < 0 {
			t.Fatal("missing clip toolbar")
		}
		bar := page[start:]
		last := -1
		for _, want := range []string{`class="clip-picker"`, `first · notes`, `pattern-choice`, `id="pattern-shared-note"`, `id="cicada-pattern-preview"`, `data-gosx-key="create-pattern-"`, `data-machine-open`, `data-pattern-management="first"`} {
			i := strings.Index(bar, want)
			if i <= last {
				t.Fatalf("toolbar control %q missing or out of order", want)
			}
			last = i
		}
		picker := bar[:strings.Index(bar, `</nav>`)]
		if strings.Count(picker, `class="pattern-choice"`) != 2 || !strings.Contains(picker, `aria-current="true"`) || !strings.Contains(picker, `second`) {
			t.Fatal("picker does not contain both patterns and the current selection")
		}
		if strings.Contains(page, `class="pattern-editor"`) {
			t.Fatal("shell retains pattern library column")
		}
		if strings.Contains(page, `class="clip-library-notice"`) {
			t.Fatal("local pattern has a library notice")
		}
	}
	p.Patterns[0].ID = "motif.idea"
	library := getPage(t, client, address+"/studio?dock=clip&pattern=motif.idea&region=detail")
	if !strings.Contains(library, `class="clip-library-notice"`) || !strings.Contains(library, `href="#pattern-variation">Make a variation to edit</a>`) {
		t.Fatal("library pattern missing variation notice")
	}
	management := strings.Index(library, `data-pattern-management="motif.idea"`)
	if management < 0 {
		t.Fatal("missing library pattern management")
	}
	tagStart := strings.LastIndex(library[:management], "<details")
	tagEnd := strings.Index(library[management:], ">")
	if tagStart < 0 || tagEnd < 0 || strings.Contains(library[tagStart:management+tagEnd], " open") {
		t.Fatal("library pattern More panel must start closed")
	}
	p.Patterns[0].ID = "first"
	legacy := getPage(t, client, address+"/?panel=patterns&pattern=first")
	if !strings.Contains(legacy, `class="pattern-editor"`) || !strings.Contains(legacy, `class="pattern-library"`) || !strings.Contains(legacy, "Create pattern") || !strings.Contains(legacy, "Use this pattern") || strings.Contains(legacy, `class="clip-toolbar"`) {
		t.Fatal("legacy pattern layout changed")
	}
}
