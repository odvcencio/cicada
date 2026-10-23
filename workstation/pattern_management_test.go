package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestPatternEditorsConnectGridAndPlayedPhrasesToTheSong(t *testing.T) {
	for _, arrangement := range []bool{false, true} {
		for _, played := range []bool{false, true} {
			name := map[bool]string{false: "scene", true: "placement"}[arrangement] + map[bool]string{false: "/grid", true: "/played"}[played]
			t.Run(name, func(t *testing.T) {
				source := "cicada 2\ntrack keys piano {}\ntrack drums drums {}\npattern fresh notes { c4 . . . }\n"
				if arrangement {
					source += "arrange { place opening keys fresh { at=@1.1.1 length=1bar } marker fresh-place { at=@2.1.1 } }\n"
				} else {
					source += "scene main { keys=off }\nsong { main*4 }\n"
				}
				score, ds := notation.Parse([]byte(source))
				p, more := project.FromScore(score)
				if p == nil || len(ds) != 0 || len(more) != 0 {
					t.Fatal(ds, more)
				}
				if played {
					p.Patterns[0].Performance = &project.Performance{LengthTicks: 4001, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 343, Velocity: 90}}}
				}
				address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Source: source, Project: p})
				page := getPage(t, client, address+"/?panel=patterns&pattern=fresh")
				management := regexp.MustCompile(`(?s)<details[^>]*data-pattern-management="fresh".*?</details>`).FindString(page)
				if management == "" {
					t.Fatal("phrase editor has no path into the song")
				}
				for _, want := range []string{"Use this pattern", "Create variation", `name="source_revision" value="all-sources"`} {
					if !strings.Contains(management, want) {
						t.Fatal("missing", want)
					}
				}
				if strings.Contains(management, `<option value="drums"`) {
					t.Fatal("incompatible drum destination offered for notes")
				}
				if arrangement {
					length := map[bool]string{false: "960ticks", true: "4001ticks"}[played]
					for _, want := range []string{"Place in song", `action="/__actions/arrange"`, `name="content" value="fresh"`, `name="newName" value="fresh-place-2"`, `name="at" value="@2.1.1"`, `name="length" value="` + length + `"`} {
						if !strings.Contains(management, want) {
							t.Fatal("missing placement control", want)
						}
					}
					if strings.Contains(management, "Assign to scene") {
						t.Fatal("named arrangement offers scene writes")
					}
				} else if !strings.Contains(management, "Assign to scene") || strings.Contains(management, "Place in song") {
					t.Fatal("scene song has the wrong destination controls")
				}
			})
		}
	}
}

func TestPatternDestinationFailuresRetainFieldsAndForwardTheSourceReceipt(t *testing.T) {
	for _, placement := range []bool{false, true} {
		t.Run(map[bool]string{false: "scene", true: "placement"}[placement], func(t *testing.T) {
			p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}, {ID: "other", Kind: "piano"}}, Patterns: []project.Pattern{{ID: "fresh", Kind: "notes", Performance: &project.Performance{LengthTicks: 4001, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 343, Velocity: 90}}}}}, Scenes: []project.Scene{{ID: "main"}, {ID: "ending"}}}
			actionName, operation := "pattern", "bind"
			if placement {
				p.Arrange = &project.Arrangement{Placements: []project.Placement{{ID: "opening", Track: "keys", Content: "fresh", LengthTicks: 3840}}}
				actionName, operation = "arrange", "append"
			}
			address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: p})
			target := "/?panel=patterns&pattern=fresh&note=0"
			page := getPage(t, client, address+target)
			fields := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"stale"}, "source_revision": {"all-sources"}, "action": {operation}, "track": {"other"}, "__gosx_return_to": {target}}
			if placement {
				fields.Set("content", "fresh")
				fields.Set("newName", "my-phrase")
				fields.Set("at", "@7.2.3")
				fields.Set("length", "4001ticks")
			} else {
				fields.Set("pattern", "fresh")
				fields.Set("scene", "ending")
				fields.Set("binding", "play")
			}
			r := post(t, client, address+"/__actions/"+actionName, fields, false, address)
			defer r.Body.Close()
			if r.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["source_revision"] != "all-sources" {
				t.Fatal("source receipt or native error redirect lost", r.StatusCode, *edits)
			}
			page = getPage(t, client, address+r.Header.Get("Location"))
			if !strings.Contains(page, "score changed") {
				t.Fatal("native error feedback missing")
			}
			management := regexp.MustCompile(`(?s)<details[^>]*data-pattern-management="fresh".*?</details>`).FindString(page)
			for _, want := range []string{`open`, `value="other" selected`, `name="newName" value="fresh-2"`} {
				if !strings.Contains(management, want) {
					t.Fatal("failed destination lost field or hid feedback", want)
				}
			}
			if placement {
				for _, want := range []string{`name="newName" value="my-phrase"`, `name="at" value="@7.2.3"`, `name="length" value="4001ticks"`} {
					if !strings.Contains(management, want) {
						t.Fatal("failed placement lost", want)
					}
				}
			} else if !strings.Contains(management, `value="ending" selected`) {
				t.Fatal("failed scene assignment lost the chosen scene")
			}
		})
	}
}

func TestPatternVariationForwardsTheDisplayedSourceReceipt(t *testing.T) {
	address, client, edits := testApp(t)
	fields := url.Values{"csrf_token": {tokenFromPage(t, getPage(t, client, address+"/?panel=patterns"))}, "revision": {"current"}, "source_revision": {"displayed-sources"}, "action": {"duplicate"}, "pattern": {"fresh"}, "newName": {"my-idea"}}
	r := post(t, client, address+"/__actions/pattern", fields, false, address)
	defer r.Body.Close()
	if r.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["source_revision"] != "displayed-sources" {
		t.Fatal("variation lost the displayed source receipt", r.StatusCode, *edits)
	}
}
