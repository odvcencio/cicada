package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
)

type gridPatternRouteCase struct {
	name, route, source string
	body                studioEdit
	edition             int
	diskSource          string
	wantStatus          int
	wantError           string
	revision            string
}

type gridPatternRouteSnapshot struct {
	Code   int
	Body   map[string]any
	Source string
	Labels []string
}

func gridPatternRouteCases(t *testing.T) []gridPatternRouteCase {
	t.Helper()
	var cases []gridPatternRouteCase
	for _, c := range patternParityCases() {
		cases = append(cases, gridPatternRouteCase{name: "pattern/" + c.name, route: "/api/pattern", source: c.source, body: c.body})
	}
	for _, c := range []struct {
		name string
		body studioEdit
	}{
		{"note", studioEdit{Pattern: "pulse"}}, {"drum", studioEdit{Pattern: "beat", Lane: "bd", Step: 1}},
		{"pitch", studioEdit{Pattern: "pulse", Step: 1, Pitch: gridParityPitch(60)}},
		{"accent", studioEdit{Pattern: "pulse", Modifier: "accent"}}, {"slide", studioEdit{Pattern: "pulse", Modifier: "slide"}},
		{"ratchet", studioEdit{Pattern: "pulse", Modifier: "ratchet"}}, {"chance", studioEdit{Pattern: "pulse", Modifier: "chance"}},
		{"both", studioEdit{Pattern: "pulse", Pitch: gridParityPitch(60), Modifier: "accent"}},
		{"outside", studioEdit{Pattern: "pulse", Step: 8}}, {"negative", studioEdit{Pattern: "pulse", Step: -1}},
		{"missing", studioEdit{Pattern: "missing"}}, {"empty", studioEdit{}},
		{"rest accent", studioEdit{Pattern: "pulse", Step: 1, Modifier: "accent"}}, {"rest ratchet", studioEdit{Pattern: "pulse", Step: 1, Modifier: "ratchet"}},
		{"bad modifier", studioEdit{Pattern: "pulse", Modifier: "bad"}}, {"bad pitch", studioEdit{Pattern: "pulse", Pitch: gridParityPitch(128)}},
		{"drum pitch", studioEdit{Pattern: "beat", Lane: "bd", Pitch: gridParityPitch(60)}},
		{"label", studioEdit{Pattern: "pulse", Label: "Authored gesture"}},
	} {
		cases = append(cases, gridPatternRouteCase{name: "toggle/" + c.name, route: "/api/toggle", source: studioScore, body: c.body})
	}
	for _, route := range []string{"/api/toggle", "/api/pattern"} {
		for _, modifier := range []string{"", "accent", "ratchet", "chance"} {
			cases = append(cases, gridPatternRouteCase{name: strings.TrimPrefix(route, "/api/") + "/shared " + modifier, route: route, source: studioPatternScore, body: studioEdit{Action: "toggle", Pattern: "p", Step: 4, Modifier: modifier}})
		}
	}
	cases = append(cases,
		gridPatternRouteCase{name: "pattern/shared pitch", route: "/api/pattern", source: studioPatternScore, body: studioEdit{Action: "pitch", Pattern: "p", Step: 4, Pitch: gridParityPitch(72)}},
		gridPatternRouteCase{name: "pattern/missing pitch", route: "/api/pattern", source: studioScore, body: studioEdit{Action: "pitch", Pattern: "pulse"}},
		gridPatternRouteCase{name: "pattern/missing pattern pitch", route: "/api/pattern", source: studioScore, body: studioEdit{Action: "pitch", Pattern: "ghost"}},
		gridPatternRouteCase{name: "pattern/unknown", route: "/api/pattern", source: studioScore, body: studioEdit{Action: "unknown"}},
		gridPatternRouteCase{name: "pattern/label", route: "/api/pattern", source: studioScore, body: studioEdit{Action: "resize", Pattern: "pulse", Length: 8, Label: "Authored pattern gesture"}},
		gridPatternRouteCase{name: "pattern/mixed toggle", route: "/api/pattern", source: strings.Replace(studioPatternScore, "use hook use hook +12", "1 . use hook", 1), body: studioEdit{Action: "toggle", Pattern: "p"}},
		gridPatternRouteCase{name: "pattern/toggle label with pitch", route: "/api/pattern", source: studioScore, body: studioEdit{Action: "toggle", Pattern: "pulse", Pitch: gridParityPitch(60)}},
		gridPatternRouteCase{name: "pattern/shared resize unchanged", route: "/api/pattern", source: studioPatternScore, body: studioEdit{Action: "resize", Pattern: "p", Length: 8}},
		gridPatternRouteCase{name: "pattern/disk invalid", route: "/api/pattern", source: studioScore, diskSource: studioScore + "???\n", body: studioEdit{Action: "settings", Pattern: "pulse", Settings: &studioPatternSettings{Swing100: 5500, Gate: 60}}},
		gridPatternRouteCase{name: "toggle/disk invalid", route: "/api/toggle", source: studioScore, diskSource: studioScore + "???\n", body: studioEdit{Pattern: "pulse"}},
	)
	for _, action := range []string{"settings", "step", "pitch", "toggle", "duplicate", "range", "resize", "bind"} {
		source := "// authored header\n" + strings.Replace(studioPatternScore, "track bass acid {}", "track bass acid {}\ntrack input audio {} // keep audio", 1)
		body := studioEdit{Action: action, Pattern: "p", Step: 1, Pitch: gridParityPitch(72), NewName: "variation", Length: 16, Scene: "other", Track: "bass", Settings: &studioPatternSettings{Swing100: 5500, Gate: 60}, NoteEdit: &studioStepEdit{Mode: "note", Pitch: 72, Ratchet: 1, Chance: 100}, Range: &studioPatternRange{Operation: "reverse", First: 0, Last: 7}}
		for _, header := range []string{"", "cicada 2\n"} {
			cases = append(cases, gridPatternRouteCase{name: fmt.Sprintf("pattern/edition2 %s %t", action, header != ""), route: "/api/pattern", source: header + source, body: body, edition: 2})
		}
	}
	// Generated with the continuity runner's --write-fixture mode. The first
	// request draws cell 15 at MIDI 54, translated to the zero-based step 14.
	continuity, err := os.ReadFile("testdata/studio-continuity.cicada")
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []struct {
		name       string
		edition    int
		headerless bool
	}{
		{"loose edition2", 0, false}, {"manifest edition2", 2, false}, {"inherited edition2", 2, true},
	} {
		source := string(continuity)
		if layout.headerless {
			source = strings.TrimPrefix(source, "cicada 2\n")
		}
		cases = append(cases, gridPatternRouteCase{name: "pattern/continuity pitch " + layout.name, route: "/api/pattern", source: source, edition: layout.edition, body: studioEdit{Action: "pitch", Pattern: "continuity", Step: 14, Pitch: gridParityPitch(54)}})
	}
	cases = append(cases, gridPatternRouteCase{name: "toggle/continuity pitch loose edition2", route: "/api/toggle", source: string(continuity), body: studioEdit{Pattern: "continuity", Step: 14, Pitch: gridParityPitch(54)}})
	// Listed parity exceptions: transposed phrase chord toggle, pitch, step,
	// range, and resize preserve all chord pitches instead of the old scalar.
	chordSource := "instrument piano { voice poly { out=sine(pitch)*env(gate,300ms) } }\ntrack keys piano {}\nphrase harmony { [d4 f4 a4]^?70 c4 }\npattern chords notes { . use harmony +12 use harmony +24 }\nscene main { keys=chords }\nsong { main }\n"
	for _, action := range []string{"toggle", "pitch", "step", "range", "resize"} {
		cases = append(cases, gridPatternRouteCase{name: "pattern/transposed phrase chord " + action, route: "/api/pattern", source: chordSource,
			body: studioEdit{Action: action, Pattern: "chords", Pitch: gridParityPitch(54), Length: 8,
				NoteEdit: &studioStepEdit{Mode: "note", Pitch: 54, Ratchet: 1, Chance: 100}, Range: &studioPatternRange{Operation: "reverse"}}})
	}
	// Every error is deliberate; a write or environment failure must never become a fixture.
	intendedErrors := map[string]struct {
		status  int
		message string
	}{
		"pattern/bad length":            {422, "patterns must have 1–64 steps"},
		"pattern/bad name":              {422, "name must start with a lowercase letter or underscore and contain at most 64 letters, digits, underscores, or hyphens"},
		"pattern/bad velocity":          {422, "choose a drum velocity from x1–x9, normal x, or accented X"},
		"pattern/disk invalid":          {422, "score must validate before editing"},
		"pattern/drum range transpose":  {422, "drum lanes cannot transpose"},
		"pattern/drum tie":              {422, "drum hits cannot tie"},
		"pattern/drum transpose":        {422, "drum lanes cannot transpose"},
		"pattern/missing pattern":       {422, "unknown declaration \"ghost\""},
		"pattern/missing pattern pitch": {422, "unknown declaration \"ghost\""},
		"pattern/missing pitch":         {422, "choose a pitch"},
		"pattern/missing scene":         {422, "unknown declaration \"ghost\""},
		"pattern/name exists":           {422, "pattern \"beat\" already exists"},
		"pattern/range copy outside":    {422, "copied range would extend past the last step"},
		"pattern/range missing":         {422, "choose a valid step range and shift −127 to +127"},
		"pattern/range mode":            {422, "choose clear, copy, reverse, rotate, or transpose"},
		"pattern/range outside":         {422, "range is outside the pattern lane"},
		"pattern/settings missing":      {422, "swing must be 50–75%, gate 10–100%, and transpose −24 to +24 semitones"},
		"pattern/step hit":              {422, "hits require a drum pattern"},
		"pattern/step missing":          {422, "choose a step, ratchet 1–8, and chance 1–100%"},
		"pattern/step mode":             {422, "choose note, hit, tie, or rest"},
		"pattern/step outside":          {422, "step is outside the authored pattern lane"},
		"pattern/unknown":               {400, "unknown pattern action"},
		"toggle/bad modifier":           {422, "a note pattern, nonnegative step, and accent or slide are required"},
		"toggle/bad pitch":              {422, "a note pattern, nonnegative step, and MIDI pitch 0–127 are required"},
		"toggle/both":                   {400, "choose one grid edit per request"},
		"toggle/disk invalid":           {422, "score must validate before a grid edit"},
		"toggle/drum pitch":             {422, "a note pattern, nonnegative step, and MIDI pitch 0–127 are required"},
		"toggle/empty":                  {422, "pattern and nonnegative step are required"},
		"toggle/missing":                {422, "unknown pattern \"missing\""},
		"toggle/negative":               {422, "pattern and nonnegative step are required"},
		"toggle/outside":                {422, "pattern pulse has no step 9 in lane "},
		"toggle/rest accent":            {422, "step 2 needs a note before setting accent"},
		"toggle/rest ratchet":           {422, "step 2 needs a note before changing ratchet"},
	}
	var expanded []gridPatternRouteCase
	for _, c := range cases {
		c.wantStatus = http.StatusOK
		if intended, ok := intendedErrors[c.name]; ok {
			c.wantStatus, c.wantError = intended.status, intended.message
		}
		for _, newline := range []string{"\n", "\r\n"} {
			v := c
			v.name += fmt.Sprintf("/%q", newline)
			v.source = strings.ReplaceAll(v.source, "\n", newline)
			v.diskSource = strings.ReplaceAll(v.diskSource, "\n", newline)
			expanded = append(expanded, v)
		}
	}
	return expanded
}

func gridPatternRoute(t *testing.T, c gridPatternRouteCase) gridPatternRouteSnapshot {
	return gridPatternRouteWithHandler(t, c, studioHandler)
}

func gridPatternRouteWithHandler(t *testing.T, c gridPatternRouteCase, factory func(string) (http.Handler, error)) gridPatternRouteSnapshot {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := studioTestPath(t, c.source)
	if c.edition != 0 {
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "cicada.mod"), []byte(fmt.Sprintf("project edits\ncicada %d\n", c.edition)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := factory(path)
	if err != nil {
		t.Fatal(err)
	}
	source := c.source
	if c.diskSource != "" {
		source = c.diskSource
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body := c.body
	body.Revision = studioRevision([]byte(source))
	if c.revision != "" {
		body.Revision = c.revision
	}
	response := studioCall(t, handler, c.route, body)
	reply := gridPatternRouteSnapshot{Code: response.Code}
	if err := json.Unmarshal(response.Body.Bytes(), &reply.Body); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reply.Source = string(after)
	// Recovery filenames are random. Check the preserved bytes before normalizing
	// the path so the fixture contains the portable response contract.
	if preserved, ok := reply.Body["preserved"].(string); ok && preserved != "" {
		before, err := os.ReadFile(preserved)
		if err != nil || !bytes.Equal(before, []byte(source)) {
			t.Fatalf("preserved score: %v, %q", err, before)
		}
		reply.Body["preserved"] = "<preserved-score>"
	}
	for _, e := range studioHistoryEdits(t, handler) {
		reply.Labels = append(reply.Labels, e.Label)
	}
	assertGridPatternCapture(t, c, reply)
	return reply
}

// This guard is also used when capturing the old handlers. It rejects unexpected
// errors before any response can be written as a golden expectation.
func assertGridPatternCapture(t *testing.T, c gridPatternRouteCase, reply gridPatternRouteSnapshot) {
	t.Helper()
	if reply.Code != c.wantStatus {
		t.Fatalf("%s: expected status %d, got %d: %+v", c.name, c.wantStatus, reply.Code, reply.Body)
	}
	if c.wantError != "" {
		wantSource := c.source
		var wantLabels []string
		if c.diskSource != "" {
			wantSource, wantLabels = c.diskSource, []string{"External file change"}
		}
		if reply.Body["error"] != c.wantError || reply.Source != wantSource || !reflect.DeepEqual(reply.Labels, wantLabels) {
			t.Fatalf("%s: intended error must preserve source and history: %+v", c.name, reply)
		}
	} else if reply.Body["error"] != nil || reply.Body["valid"] != true {
		t.Fatalf("%s: expected a valid success: %+v", c.name, reply)
	}
}

func TestGridPatternCaptureInventory(t *testing.T) {
	data, err := os.ReadFile("testdata/grid-pattern-route-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]gridPatternRouteSnapshot
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	cases := gridPatternRouteCases(t)
	if len(cases) != 198 || len(golden) != len(cases) {
		t.Fatalf("capture inventory: %d cases, %d snapshots", len(cases), len(golden))
	}
	var successes, intendedErrors int
	for _, c := range cases {
		reply, ok := golden[c.name]
		if !ok {
			t.Fatalf("missing old-handler capture %q", c.name)
		}
		assertGridPatternCapture(t, c, reply)
		if c.wantError == "" {
			successes++
		} else {
			intendedErrors++
		}
	}
	t.Logf("old-handler captures: %d successes, %d intended errors", successes, intendedErrors)
}

func assertGridPatternRoutes(t *testing.T, route string) {
	t.Helper()
	data, err := os.ReadFile("testdata/grid-pattern-route-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]gridPatternRouteSnapshot
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	for _, c := range gridPatternRouteCases(t) {
		if c.route != route {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			want, ok := golden[c.name]
			if !ok {
				t.Fatalf("missing pre-port capture %q", c.name)
			}
			assertGridPatternCapture(t, c, want)
			old := gridPatternRouteWithHandler(t, c, legacyGridPatternHandler)
			if !reflect.DeepEqual(old, want) {
				t.Fatalf("old handler changed since capture\n got: %+v\nwant: %+v", old, want)
			}
			want = gridPatternParityExpectation(t, c, want)
			got := gridPatternRoute(t, c)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("route parity\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestRouteParity_Toggle(t *testing.T)  { assertGridPatternRoutes(t, "/api/toggle") }
func TestRouteParity_Pattern(t *testing.T) { assertGridPatternRoutes(t, "/api/pattern") }

func TestGridPatternRouteConflictParity(t *testing.T) {
	for _, route := range []string{"/api/toggle", "/api/pattern"} {
		t.Run(route, func(t *testing.T) {
			c := gridPatternRouteCase{name: route + "/stale revision", route: route, source: studioScore,
				body: studioEdit{Action: "toggle", Pattern: "pulse"}, revision: "stale", wantStatus: http.StatusConflict,
				wantError: "score changed on disk; reload before saving"}
			old := gridPatternRouteWithHandler(t, c, legacyGridPatternHandler)
			got := gridPatternRoute(t, c)
			if got.Code != old.Code || got.Body["error"] != old.Body["error"] || got.Source != old.Source || !reflect.DeepEqual(got.Labels, old.Labels) {
				t.Fatalf("conflict parity: got %+v, want %+v", got, old)
			}
			// The M1 intent adapter also returns the current state on a revision conflict.
			if got.Body["source"] != studioScore || got.Body["revision"] != studioRevision([]byte(studioScore)) {
				t.Fatalf("canonical conflict state: %+v", got.Body)
			}
		})
	}
}

func TestGridPatternRoutesCommitPublicIntents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, c := range []struct {
		route, kind, shared string
		body                studioEdit
	}{
		{"/api/toggle", "togglestep", "definition", studioEdit{Pattern: "pulse"}},
		{"/api/toggle", "setpitch", "definition", studioEdit{Pattern: "pulse", Step: 1, Pitch: gridParityPitch(60)}},
		{"/api/pattern", "togglestep", "pattern", studioEdit{Action: "toggle", Pattern: "pulse"}},
		{"/api/pattern", "setstep", "pattern", studioEdit{Action: "step", Pattern: "pulse", Step: 1, NoteEdit: &studioStepEdit{Mode: "note", Pitch: 62, Ratchet: 1, Chance: 100}}},
		{"/api/pattern", "setrange", "pattern", studioEdit{Action: "range", Pattern: "pulse", Range: &studioPatternRange{Operation: "reverse", Last: 3}}},
		{"/api/pattern", "resizepattern", "pattern", studioEdit{Action: "resize", Pattern: "pulse", Length: 8}},
	} {
		t.Run(c.route+"/"+c.kind, func(t *testing.T) {
			handler, path := studioTestHandler(t)
			body := c.body
			body.Revision = studioRevision([]byte(studioScore))
			body.Author = "tester"
			body.Session = "s1"
			r := studioCall(t, handler, c.route, body)
			if r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			records, skipped, err := editlog.ReadLog(editlog.Path(path))
			if err != nil || skipped != 0 || len(records) != 1 || len(records[0].Intents) != 1 {
				t.Fatalf("intent log: %+v, %v", records, err)
			}
			var in map[string]any
			if err := json.Unmarshal(records[0].Intents[0], &in); err != nil {
				t.Fatal(err)
			}
			if in["kind"] != c.kind || in["shared"] != c.shared || records[0].Author != "tester" || records[0].Session != "s1" {
				t.Fatalf("public intent attribution/policy: %+v %+v", records[0], in)
			}
		})
	}
}

func TestContinuityPatternRouteParity(t *testing.T) {
	for _, c := range gridPatternRouteCases(t) {
		if !strings.Contains(c.name, "/continuity ") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			old := gridPatternRouteWithHandler(t, c, legacyGridPatternHandler)
			got := gridPatternRoute(t, c)
			if !reflect.DeepEqual(got, old) {
				t.Fatalf("continuity route parity: got %+v, want %+v", got, old)
			}
		})
	}
}

// Only these named transposed-chord cases differ from the legacy captures.
// Byte, response, and label parity still apply everywhere else.
func gridPatternParityExpectation(t *testing.T, c gridPatternRouteCase, old gridPatternRouteSnapshot) gridPatternRouteSnapshot {
	t.Helper()
	if !strings.HasPrefix(c.name, "pattern/transposed phrase chord ") {
		return old
	}
	want := old
	want.Body = make(map[string]any, len(old.Body))
	for key, value := range old.Body {
		want.Body[key] = value
	}
	for scalar, chord := range map[string]string{"d5^?70": "[d5 f5 a5]^?70", "d6^?70": "[d6 f6 a6]^?70"} {
		if strings.Count(want.Source, scalar) != 1 {
			t.Fatalf("parity exception %s lacks the old scalar %q", c.name, scalar)
		}
		want.Source = strings.Replace(want.Source, scalar, chord, 1)
	}
	want.Body["source"] = want.Source
	want.Body["revision"] = studioRevision([]byte(want.Source))
	want.Body["playingRevision"] = want.Body["revision"]
	return want
}
