package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestNotesActionForwardsOneCheckedBatch(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	target := patternURL("p", 1, "", "")
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "source_revision": {"current-sources"}, "pattern": {"p"}, "op": {"quantize"}, "grid": {"240"}, "strength": {"60"}, "__gosx_return_to": {target}}
	r := post(t, client, address+"/__actions/notes", form, false, address)
	if r.StatusCode != 303 || r.Header.Get("Location") != target || len(*edits) != 1 {
		t.Fatalf("quantize form: %d %s %+v", r.StatusCode, r.Header.Get("Location"), *edits)
	}
	sent := (*edits)[0]
	notes, _ := sent["notes"].(map[string]any)
	if sent["action"] != "notes" || sent["pattern"] != "p" || sent["revision"] != "current" || sent["source_revision"] != "current-sources" ||
		notes["op"] != "quantize" || notes["grid"] != float64(240) || notes["strength"] != float64(60) || notes["entity"] != "" && notes["entity"] != nil {
		t.Fatalf("forwarded quantize: %+v", sent)
	}
	// The engine's JSON batch, as the design spells it, is forwarded intact.
	form.Del("op")
	form.Del("grid")
	form.Del("strength")
	form.Set("edit", `{"v":1,"op":"move","notes":[{"at":480,"pitch":57,"lane":"","length":240,"velocity":96}],"dt":240,"dp":2,"dl":0,"dv":0,"grid":240,"copy":false,"insert":[]}`)
	if r := post(t, client, address+"/__actions/notes", form, true, address); r.StatusCode != 303 || len(*edits) != 2 {
		t.Fatalf("JSON batch: %d %+v", r.StatusCode, *edits)
	}
	moved := (*edits)[1]["notes"].(map[string]any)
	refs := moved["notes"].([]any)
	if moved["op"] != "move" || moved["dt"] != float64(240) || moved["dp"] != float64(2) || len(refs) != 1 || refs[0].(map[string]any)["pitch"] != float64(57) || refs[0].(map[string]any)["velocity"] != float64(96) {
		t.Fatalf("forwarded move: %+v", moved)
	}
	many := make([]string, 513)
	for i := range many {
		many[i] = fmt.Sprintf(`{"at":%d,"pitch":60,"length":240,"velocity":96}`, i*240)
	}
	for name, edit := range map[string]string{
		"version":       `{"v":2,"op":"delete","notes":[{"at":0,"pitch":60,"length":240,"velocity":96}]}`,
		"unknown op":    `{"v":1,"op":"stretch"}`,
		"unknown field": `{"v":1,"op":"quantize","grid":240,"swing":true}`,
		"entity":        `{"v":1,"op":"quantize","grid":240,"entity":"pattern:other"}`,
		"too many":      `{"v":1,"op":"delete","notes":[` + strings.Join(many, ",") + `]}`,
		"pitch":         `{"v":1,"op":"delete","notes":[{"at":0,"pitch":128,"length":240,"velocity":96}]}`,
		"range":         `{"v":1,"op":"move","notes":[{"at":0,"pitch":60,"length":240,"velocity":96}],"dp":200}`,
		"no notes":      `{"v":1,"op":"move","dt":240}`,
		"not JSON":      `{"v":1,`,
	} {
		form.Set("edit", edit)
		if r := post(t, client, address+"/__actions/notes", form, true, address); r.StatusCode != 422 || len(*edits) != 2 {
			t.Fatalf("%s reached the service: %d", name, r.StatusCode)
		}
	}
	form.Del("edit")
	for name, values := range map[string]url.Values{
		"op":       {"op": {"move"}, "grid": {"240"}},
		"grid":     {"op": {"quantize"}, "grid": {"7"}},
		"strength": {"op": {"quantize"}, "grid": {"240"}, "strength": {"0"}},
		"pattern":  {"op": {"quantize"}, "grid": {"240"}, "pattern": {""}},
	} {
		attempt := url.Values{}
		for key, value := range form {
			attempt[key] = value
		}
		for key, value := range values {
			attempt[key] = value
		}
		if r := post(t, client, address+"/__actions/notes", attempt, true, address); r.StatusCode != 422 || len(*edits) != 2 {
			t.Fatalf("form %s reached the service: %d", name, r.StatusCode)
		}
	}
	form.Set("op", "quantize")
	form.Set("grid", "240")
	form.Set("revision", "stale")
	if r := post(t, client, address+"/__actions/notes", form, true, address); r.StatusCode != 409 {
		t.Fatalf("a stale quantize was not refused: %d", r.StatusCode)
	}
}

func notesProject() *project.Project {
	return &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}, {ID: "drums", Kind: "drums"}},
		Patterns: []project.Pattern{
			{ID: "played", Kind: "notes", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 720, Velocity: 90}}}},
			{ID: "long", Kind: "notes", Performance: &project.Performance{LengthTicks: 7680, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 720, Velocity: 90}}}},
			{ID: "riff", Kind: "notes", Steps: 32, Data: append([]*project.Step{{Note: 60, Velocity: 100, Ratchet: 1, Probability: 100}}, make([]*project.Step, 31)...)},
			{ID: "beat", Kind: "notes", Steps: 16, Data: append([]*project.Step{{Note: 60, Velocity: 100, Ratchet: 1, Probability: 100}}, make([]*project.Step, 15)...)},
			{ID: "slow", Kind: "drums", Steps: 8, StepTicks: 960, Lanes: map[string][]*project.Step{"bd": append([]*project.Step{{Note: 36, Velocity: 100, Ratchet: 1, Probability: 100}}, make([]*project.Step, 7)...)}},
			{ID: "bars", Kind: "drums", Steps: 2, StepTicks: 3840, Lanes: map[string][]*project.Step{"bd": {{Note: 36, Velocity: 100, Ratchet: 1, Probability: 100}, nil}}},
		}}
}

func notesQuantizeForm(page string) string {
	return regexp.MustCompile(`(?s)<div[^>]*class="notes-quantize".*?</p>`).FindString(page)
}

func TestClipTabRendersQuantizeForEveryPatternKind(t *testing.T) {
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: notesProject()})
	played := notesQuantizeForm(getPage(t, client, address+"/studio?dock=clip&pattern=played&track=keys&scene=main"))
	for _, want := range []string{`id="notes-quantize"`, `action="/__actions/notes"`, `aria-labelledby="notes-quantize-title"`, `>Quantize</h4>`, `name="op" value="quantize"`, `name="pattern" value="played"`,
		`name="source_revision" value="all-sources"`, `<option value="240" selected`, `>1/16T</option>`, `>Beat</option>`, `name="strength"`, `aria-keyshortcuts="Control+U Meta+U"`, `role="status"`, `scene=main`, `track=keys`} {
		if !strings.Contains(played, want) {
			t.Fatalf("performance Quantize lacks %s:\n%s", want, played)
		}
	}
	// Grids as long as the pattern are left out; the redirect carries the
	// editor's track and scene, so the form has no fields of its own for them.
	if strings.Contains(played, `>Bar</option>`) || strings.Contains(played, `name="track"`) || strings.Contains(played, `name="scene"`) {
		t.Fatalf("a one-bar loop must not offer Bar, and the form forwards no context fields:\n%s", played)
	}
	if long := notesQuantizeForm(getPage(t, client, address+"/studio?dock=clip&pattern=long")); !strings.Contains(long, `>Bar</option>`) {
		t.Fatalf("a two-bar loop offers Bar:\n%s", long)
	}
	if beat := notesQuantizeForm(getPage(t, client, address+"/studio?dock=clip&pattern=beat")); !strings.Contains(beat, `<option value="960" selected`) || strings.Contains(beat, `>Bar</option>`) {
		t.Fatalf("a one-bar step pattern offers Beat only:\n%s", beat)
	}
	riff := notesQuantizeForm(getPage(t, client, address+"/studio?dock=clip&pattern=riff"))
	if !strings.Contains(riff, `<option value="960" selected`) || !strings.Contains(riff, `>Bar</option>`) || strings.Contains(riff, `name="strength"`) || strings.Contains(riff, `>1/16</option>`) {
		t.Fatalf("step-pattern Quantize must offer Beat and Bar at full strength:\n%s", riff)
	}
	slow := notesQuantizeForm(getPage(t, client, address+"/studio?dock=clip&pattern=slow"))
	if !strings.Contains(slow, `<option value="3840" selected`) || strings.Contains(slow, `>Beat</option>`) {
		t.Fatalf("a quarter-note drum grid offers only Bar:\n%s", slow)
	}
	bars := notesQuantizeForm(getPage(t, client, address+"/studio?dock=clip&pattern=bars"))
	if strings.Contains(bars, "<form") || !strings.Contains(bars, "No grid is coarser than this pattern") {
		t.Fatalf("a bar grid has nothing coarser to quantize to:\n%s", bars)
	}
	// The shortcuts dialog lists Quantize once, as a working command.
	dialog := regexp.MustCompile(`(?s)<[a-z]+[^>]*id="palette".*`).FindString(getPage(t, client, address+"/studio"))
	row := regexp.MustCompile(`(?s)<li>\s*<span class="shortcut-title">Quantize notes</span>.*?</li>`).FindAllString(dialog, -1)
	if len(row) != 1 || strings.Contains(row[0], "Unavailable") || !strings.Contains(row[0], "Control+U") {
		t.Fatalf("shortcuts dialog Quantize rows: %q", row)
	}
}

func TestNotesActionReportsAnUnchangedPatternAndFailures(t *testing.T) {
	var sent []map[string]any
	reply := `{"revision":"current","valid":true,"unchanged":true,"message":"Already on the grid."}`
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: notesProject()})
		case r.URL.Path == "/api/pattern" && r.Method == http.MethodPost:
			var edit map[string]any
			_ = json.NewDecoder(r.Body).Decode(&edit)
			sent = append(sent, edit)
			if strings.Contains(reply, `"error"`) {
				w.WriteHeader(http.StatusUnprocessableEntity)
			}
			_, _ = io.WriteString(w, reply)
		case r.URL.Path == "/api/transport":
			_ = json.NewEncoder(w).Encode(transport{Bar: 1, Step: 1})
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(audio.Close)
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getPage(t, client, app.URL+"/?panel=patterns&pattern=played")
	target := patternURL("played", 1, "", "")
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"all-sources"}, "pattern": {"played"}, "op": {"quantize"}, "grid": {"240"}, "__gosx_return_to": {target}}
	r := post(t, client, app.URL+"/__actions/notes", form, false, app.URL)
	if r.StatusCode != 303 || len(sent) != 1 {
		t.Fatalf("unchanged quantize: %d", r.StatusCode)
	}
	if after := getPage(t, client, app.URL+r.Header.Get("Location")); !strings.Contains(after, "Already on the grid.") {
		t.Fatal("an unchanged quantize did not say why")
	}
	reply = `{"error":"two C4 notes would overlap at tick 240; a pitch plays one note at a time, so choose a finer grid"}`
	form.Set("grid", "960")
	r = post(t, client, app.URL+"/__actions/notes", form, false, app.URL)
	if r.StatusCode != 303 || len(sent) != 2 {
		t.Fatalf("refused quantize: %d", r.StatusCode)
	}
	failed := notesQuantizeForm(getPage(t, client, app.URL+r.Header.Get("Location")))
	if !strings.Contains(failed, "two C4 notes would overlap") || !strings.Contains(failed, `<option value="960" selected`) {
		t.Fatalf("a refused quantize hid its reason or lost the chosen grid:\n%s", failed)
	}
}

// The workbench runs the first available command for a chord. The live
// Quantize must come before its reserved mirror, or the mirror would consume
// Mod+U even in the Clip tab; and the live one must require the Clip tab to be
// on screen, because $dock stays "clip" while the narrow layout shows another
// region.
func TestQuantizeCommandPrecedesItsMirror(t *testing.T) {
	live, mirror := -1, -1
	for i, cmd := range shellCommands() {
		switch cmd.ID {
		case "notes.quantize":
			live = i
			if cmd.When["$notesVisible"] != true || cmd.When["$dock"] != "clip" || cmd.Reserved {
				t.Fatalf("Quantize must need the visible Clip tab: %+v", cmd)
			}
		case "notes.quantize.elsewhere":
			mirror = i
			if !cmd.Reserved || len(cmd.When) != 0 || strings.Join(cmd.Keys, ",") != "Mod+U" {
				t.Fatalf("the mirror must reserve Mod+U unconditionally: %+v", cmd)
			}
		}
	}
	if live < 0 || mirror < 0 || live > mirror {
		t.Fatalf("notes.quantize at %d must precede notes.quantize.elsewhere at %d", live, mirror)
	}
}
