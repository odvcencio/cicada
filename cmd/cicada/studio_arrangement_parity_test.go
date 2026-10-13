package main

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
)

// arrangementParityCases cover every /api/arrange action, success and refusal.
func arrangementParityCases() []struct {
	name   string
	source string
	edit   studioEdit
} {
	place := func(at, length, track, content string) *studioPlaceSettings {
		return &studioPlaceSettings{At: at, Length: length, Track: track, Content: content}
	}
	noArrange := "cicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\nscene main { bass=pulse }\nsong { main }\n"
	oneOnly := "cicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\narrange {\n  place one bass pulse { at = @1.1.1 length = 4bars }\n}\n"
	noNewline := "cicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\narrange { place one bass pulse { at = @1.1.1 length = 4bars } }"
	noFields := "cicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\narrange {\n  place one bass pulse { }\n  marker m { }\n}\n"
	cases := []struct {
		name   string
		source string
		edit   studioEdit
	}{
		{"update move and trim", studioPlacementScore, studioEdit{Action: "update", Name: "one", Placement: place("@2.1.1", "3bars", "", "")}},
		{"update track and content", studioPlacementScore, studioEdit{Action: "update", Name: "two", Placement: place("", "", "bass", "pulse")}},
		{"update length only", studioPlacementScore, studioEdit{Action: "update", Name: "two", Placement: place("", "2bars", "", "")}},
		{"update dotted reference", studioPlacementScore, studioEdit{Action: "update", Name: "two", Placement: place("", "", "bass", "lib.pulse")}},
		{"update crlf", strings.ReplaceAll(studioPlacementScore, "\n", "\r\n"), studioEdit{Action: "update", Name: "one", Placement: place("@2.1.1", "", "", "")}},
		{"update no settings", studioPlacementScore, studioEdit{Action: "update", Name: "one"}},
		{"update empty settings", studioPlacementScore, studioEdit{Action: "update", Name: "one", Placement: place("", "", "", "")}},
		{"update unknown", studioPlacementScore, studioEdit{Action: "update", Name: "ghost", Placement: place("@1.1.1", "", "", "")}},
		{"update empty name", studioPlacementScore, studioEdit{Action: "update", Placement: place("@1.1.1", "", "", "")}},
		{"update slash name", studioPlacementScore, studioEdit{Action: "update", Name: "a/b", Placement: place("@1.1.1", "", "", "")}},
		{"update marker name as placement", studioPlacementScore, studioEdit{Action: "update", Name: "return", Placement: place("@1.1.1", "", "", "")}},
		{"update bad start", studioPlacementScore, studioEdit{Action: "update", Name: "one", Placement: place("soon", "", "", "")}},
		{"update bad length", studioPlacementScore, studioEdit{Action: "update", Name: "one", Placement: place("", "1/7", "", "")}},
		{"update bad reference", studioPlacementScore, studioEdit{Action: "update", Name: "one", Placement: place("", "", "bass\nmarker injected { at = @1.1.1 }", "")}},
		{"update missing field", noFields, studioEdit{Action: "update", Name: "one", Placement: place("@2.1.1", "", "", "")}},
		{"update missing length field", noFields, studioEdit{Action: "update", Name: "one", Placement: place("", "2bars", "", "")}},
		{"update no arrange", noArrange, studioEdit{Action: "update", Name: "one", Placement: place("@2.1.1", "", "", "")}},
		{"update syntax error", "cicada 2\narrange {{{ \n", studioEdit{Action: "update", Name: "one", Placement: place("@2.1.1", "", "", "")}},
		{"append", studioPlacementScore, studioEdit{Action: "append", NewName: "ending", Placement: place("@9.1.1", "320ticks", "bass", "pulse")}},
		{"append crlf", strings.ReplaceAll(studioPlacementScore, "\n", "\r\n"), studioEdit{Action: "append", NewName: "ending", Placement: place("@9.1.1", "1bars", "bass", "pulse")}},
		{"append inline block", noNewline, studioEdit{Action: "append", NewName: "ending", Placement: place("@9.1.1", "1bars", "bass", "pulse")}},
		{"append taken name", studioPlacementScore, studioEdit{Action: "append", NewName: "return", Placement: place("@9.1.1", "1bars", "bass", "pulse")}},
		{"append bad name", studioPlacementScore, studioEdit{Action: "append", NewName: "Bad Name", Placement: place("@9.1.1", "1bars", "bass", "pulse")}},
		{"append no start", studioPlacementScore, studioEdit{Action: "append", NewName: "ending", Placement: place("", "1bars", "bass", "pulse")}},
		{"append no track", studioPlacementScore, studioEdit{Action: "append", NewName: "ending", Placement: place("@9.1.1", "1bars", "", "pulse")}},
		{"append no settings", studioPlacementScore, studioEdit{Action: "append", NewName: "ending"}},
		{"append no arrange", noArrange, studioEdit{Action: "append", NewName: "ending", Placement: place("@9.1.1", "1bars", "bass", "pulse")}},
		{"duplicate", studioPlacementScore, studioEdit{Action: "duplicate", Name: "one", NewName: "one-copy", Placement: place("@9.1.1", "", "", "")}},
		{"duplicate override fields", studioPlacementScore, studioEdit{Action: "duplicate", Name: "one", NewName: "one-copy", Placement: place("@9.1.1", "1bars", "bass", "pulse")}},
		{"duplicate crlf", strings.ReplaceAll(studioPlacementScore, "\n", "\r\n"), studioEdit{Action: "duplicate", Name: "two", NewName: "two-copy", Placement: place("@9.1.1", "", "", "")}},
		{"duplicate no start", studioPlacementScore, studioEdit{Action: "duplicate", Name: "one", NewName: "one-copy", Placement: place("", "", "", "")}},
		{"duplicate taken", studioPlacementScore, studioEdit{Action: "duplicate", Name: "one", NewName: "two", Placement: place("@9.1.1", "", "", "")}},
		{"duplicate unknown", studioPlacementScore, studioEdit{Action: "duplicate", Name: "ghost", NewName: "copy", Placement: place("@9.1.1", "", "", "")}},
		{"duplicate missing field", noFields, studioEdit{Action: "duplicate", Name: "one", NewName: "copy", Placement: place("@9.1.1", "", "", "")}},
		{"delete", studioPlacementScore, studioEdit{Action: "delete", Name: "two"}},
		{"delete keeps comment", studioPlacementScore, studioEdit{Action: "delete", Name: "one"}},
		{"delete last", oneOnly, studioEdit{Action: "delete", Name: "one"}},
		{"delete unknown", studioPlacementScore, studioEdit{Action: "delete", Name: "ghost"}},
		{"marker move", studioPlacementScore, studioEdit{Action: "marker", Name: "return", Placement: place("@9.1.1", "", "", "")}},
		{"marker length", studioPlacementScore, studioEdit{Action: "marker", Name: "return", Placement: place("", "1bars", "", "")}},
		{"marker track", studioPlacementScore, studioEdit{Action: "marker", Name: "return", Placement: place("@9.1.1", "", "bass", "")}},
		{"marker unknown", studioPlacementScore, studioEdit{Action: "marker", Name: "one", Placement: place("@9.1.1", "", "", "")}},
		{"marker no settings", studioPlacementScore, studioEdit{Action: "marker", Name: "return"}},
		{"marker empty settings", studioPlacementScore, studioEdit{Action: "marker", Name: "return", Placement: place("", "", "", "")}},
		{"append marker", studioPlacementScore, studioEdit{Action: "append-marker", NewName: "finish", Placement: place("@10.1.1", "", "", "")}},
		{"append marker crlf", strings.ReplaceAll(studioPlacementScore, "\n", "\r\n"), studioEdit{Action: "append-marker", NewName: "finish", Placement: place("@10.1.1", "", "", "")}},
		{"append marker taken", studioPlacementScore, studioEdit{Action: "append-marker", NewName: "one", Placement: place("@10.1.1", "", "", "")}},
		{"append marker length", studioPlacementScore, studioEdit{Action: "append-marker", NewName: "finish", Placement: place("@10.1.1", "1bars", "", "")}},
		{"append marker content", studioPlacementScore, studioEdit{Action: "append-marker", NewName: "finish", Placement: place("@10.1.1", "", "", "pulse")}},
		{"delete marker", studioPlacementScore, studioEdit{Action: "delete-marker", Name: "return"}},
		{"delete marker unknown", studioPlacementScore, studioEdit{Action: "delete-marker", Name: "one"}},
		{"delete marker no arrange", noArrange, studioEdit{Action: "delete-marker", Name: "return"}},
	}
	return cases
}

func TestArrangementIntentsMatchTheOldWriter(t *testing.T) {
	recorder := newM10Recorder(t, "arrangement")
	for _, c := range arrangementParityCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want, wantErr := editedArrangementSource([]byte(c.source), c.edit)
			recorder.add(c.name, want, wantErr)
			intent, ok := arrangementIntent(c.edit)
			if !ok {
				t.Fatalf("no intent for %q", c.edit.Action)
			}
			// Compare the text edit alone: the old writer never compiled.
			got, gotErr := edits.Apply([]byte(c.source), edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}, edits.Options{Compiler: arrangementTextOnly{}})
			if (wantErr == nil) != (gotErr == nil) || wantErr != nil && wantErr.Error() != gotErr.Error() {
				t.Fatalf("error %v, want %v", gotErr, wantErr)
			}
			if wantErr == nil && !bytes.Equal(got.Source, want) {
				t.Fatalf("bytes differ\n got: %q\nwant: %q", got.Source, want)
			}
		})
	}
}

// arrangementTextOnly accepts any candidate, so the comparison covers only the
// text edit the old writer produced.
type arrangementTextOnly struct{}

func (arrangementTextOnly) Compile(source []byte, _ map[string][]byte) (*edits.Plan, error) {
	return &edits.Plan{Revision: edits.Revision(source)}, nil
}

// Every /api/arrange action records its intent in the edit log and keeps the
// history label the route always produced.
func TestArrangeRouteLogsIntentsAndKeepsLabels(t *testing.T) {
	for _, c := range []struct {
		edit  studioEdit
		kind  string
		label string
	}{
		{studioEdit{Action: "update", Name: "two", Placement: &studioPlaceSettings{At: "@6.1.1"}}, "updateplacement", "Source saved"},
		{studioEdit{Action: "delete", Name: "two"}, "removeplacement", "Arrangement · block 1 · delete"},
		{studioEdit{Action: "marker", Name: "return", Placement: &studioPlaceSettings{At: "@9.1.1"}}, "setmarker", "Source saved"},
		{studioEdit{Action: "append-marker", NewName: "finish", Placement: &studioPlaceSettings{At: "@10.1.1"}}, "addmarker", "Source saved"},
	} {
		path := studioTestPath(t, studioPlacementScore)
		handler, err := studioHandler(path)
		if err != nil {
			t.Fatal(err)
		}
		c.edit.Revision = studioRevision([]byte(studioPlacementScore))
		if response := studioCall(t, handler, "/api/arrange", c.edit); response.Code != 200 {
			t.Fatalf("%s: %d %s", c.edit.Action, response.Code, response.Body.String())
		}
		records, _, err := editlog.ReadLog(editlog.Path(path))
		if err != nil || len(records) != 1 || len(records[0].Intents) != 1 || !strings.Contains(string(records[0].Intents[0]), `"kind":"`+c.kind+`"`) || records[0].Label != c.label {
			t.Fatalf("%s: %+v %v", c.edit.Action, records, err)
		}
	}
}
