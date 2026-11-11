package agentsuite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/agentguide"
)

// quickStartPage is the page cicada agents prints.
const quickStartPage = "../../docs/manual/agent-quickstart.md"

// quickStart drives the binary as a new agent does: it knows only the text
// cicada agents printed. Every command, field and refusal the task relies on
// must be named in that text (see uses), so the page cannot stop teaching
// what the loop needs.
type quickStart struct {
	t        *testing.T
	bin      string
	guide    string
	dir      string
	commands int
}

// uses fails unless the quick start names each term, as written, in
// backticks.
func (q *quickStart) uses(terms ...string) {
	q.t.Helper()
	for _, term := range terms {
		if !strings.Contains(q.guide, "`"+term+"`") {
			q.t.Errorf("the task relies on `%s`, which the quick start does not name", term)
		}
	}
}

func (q *quickStart) run(args ...string) ([]byte, int) {
	q.t.Helper()
	q.commands++
	return cicada(q.t, q.bin, q.dir, args...)
}

func (q *quickStart) read(name string) []byte {
	q.t.Helper()
	data, err := os.ReadFile(filepath.Join(q.dir, name))
	if err != nil {
		q.t.Fatal(err)
	}
	return data
}

func (q *quickStart) write(name string, data []byte) {
	q.t.Helper()
	if err := os.WriteFile(filepath.Join(q.dir, name), data, 0o644); err != nil {
		q.t.Fatal(err)
	}
}

// block returns the fenced block that follows marker in the quick start.
func (q *quickStart) block(marker string) string {
	q.t.Helper()
	fence := strings.Repeat("`", 3)
	_, after, found := strings.Cut(q.guide, marker+"\n")
	if !found {
		q.t.Fatalf("the quick start has no %s", marker)
	}
	_, body, found := strings.Cut(after, fence)
	if !found {
		q.t.Fatalf("no fenced block after %s", marker)
	}
	_, body, _ = strings.Cut(body, "\n")
	body, _, found = strings.Cut(body, fence)
	if !found {
		q.t.Fatalf("unterminated block after %s", marker)
	}
	return body
}

func revisionOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type quickCheck struct {
	Schema string `json:"schema"`
	Valid  bool   `json:"valid"`
	Scores []struct {
		File     string `json:"file"`
		Revision string `json:"revision"`
		Valid    bool   `json:"valid"`
	} `json:"scores"`
}

// check runs the loop's step 3 and returns the score's revision.
func (q *quickStart) check() string {
	q.t.Helper()
	var check quickCheck
	stdout, code := q.run("check", "--json", "main.cicada")
	decode(q.t, "check --json", stdout, &check)
	if code != 0 || check.Schema != "cicada.check.v1" || !check.Valid || len(check.Scores) != 1 || !check.Scores[0].Valid {
		q.t.Fatalf("check --json: exit %d %+v", code, check)
	}
	if want := revisionOf(q.read("main.cicada")); check.Scores[0].Revision != want {
		q.t.Fatalf("check --json: revision %q is not the SHA-256 of the file, %s", check.Scores[0].Revision, want)
	}
	return check.Scores[0].Revision
}

type quickApply struct {
	Schema        string `json:"schema"`
	OK            bool   `json:"ok"`
	Written       bool   `json:"written"`
	RevisionAfter string `json:"revision_after"`
	Diff          string `json:"diff"`
	Error         *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Index   *int   `json:"index"`
		Kind    string `json:"kind"`
		Field   string `json:"field"`
	} `json:"error"`
}

// apply writes envelope to edit.json and applies it, writing when write is
// set.
func (q *quickStart) apply(envelope any, write bool) (quickApply, int) {
	q.t.Helper()
	data, err := json.Marshal(envelope)
	if err != nil {
		q.t.Fatal(err)
	}
	q.write("edit.json", data)
	args := []string{"apply", "main.cicada", "edit.json", "--json"}
	if write {
		args = append(args, "--write")
	}
	var result quickApply
	stdout, code := q.run(args...)
	decode(q.t, "apply --json", stdout, &result)
	if result.Schema != "cicada.apply.v1" {
		q.t.Fatalf("apply --json: schema %q", result.Schema)
	}
	return result, code
}

type quickRender struct {
	Schema  string `json:"schema"`
	OK      bool   `json:"ok"`
	Bars    int    `json:"bars"`
	Outputs []struct {
		Path string `json:"path"`
	} `json:"outputs"`
	Levels *struct {
		ClippedCount *int `json:"clipped_count"`
	} `json:"levels"`
	Loudness *struct {
		TargetLUFS      float64  `json:"target_lufs"`
		ToleranceLU     float64  `json:"tolerance_lu"`
		TruePeakMaxDBTP float64  `json:"true_peak_max_dbtp"`
		IntegratedLUFS  *float64 `json:"integrated_lufs"`
		TruePeakDBTP    *float64 `json:"true_peak_dbtp"`
		TargetMet       bool     `json:"target_met"`
	} `json:"loudness"`
	Error *struct {
		Code   string `json:"code"`
		Remedy *struct {
			Revision  string `json:"revision"`
			Replaces  string `json:"replaces"`
			ScoreText string `json:"score_text"`
		} `json:"remedy"`
	} `json:"error"`
}

func (q *quickStart) render(args ...string) (quickRender, int) {
	q.t.Helper()
	var result quickRender
	stdout, code := q.run(append([]string{"render", "main.cicada", "-o", "mix.wav"}, append(args, "--json")...)...)
	decode(q.t, "render --json", stdout, &result)
	if result.Schema != "cicada.render.v1" {
		q.t.Fatalf("render --json: schema %q", result.Schema)
	}
	return result, code
}

// rendersToTarget renders to -14 LUFS, requires the target met, and checks
// what the quick start says target_met means.
func (q *quickStart) rendersToTarget() {
	q.t.Helper()
	rendered, code := q.render("--loudness", "-14")
	loudness := rendered.Loudness
	if code != 0 || !rendered.OK || loudness == nil || !loudness.TargetMet || len(rendered.Outputs) != 1 || rendered.Outputs[0].Path != "mix.wav" {
		q.t.Fatalf("render --loudness -14: exit %d %+v %+v", code, rendered, loudness)
	}
	if math.Abs(*loudness.IntegratedLUFS-loudness.TargetLUFS) > loudness.ToleranceLU || *loudness.TruePeakDBTP > loudness.TruePeakMaxDBTP {
		q.t.Fatalf("target_met is true at %.2f LUFS and %.2f dBTP", *loudness.IntegratedLUFS, *loudness.TruePeakDBTP)
	}
}

type quickAudition struct {
	Schema string `json:"schema"`
	Score  struct {
		WrittenKey string `json:"written_key"`
	} `json:"score"`
	HeardKey []struct {
		Key string `json:"key"`
	} `json:"heard_key"`
	HarmonyMismatches *int `json:"harmony_mismatches"`
	Bars              []struct {
		Match string `json:"match"`
	} `json:"bars"`
	Tracks []struct {
		ID               string   `json:"id"`
		Silent           *bool    `json:"silent"`
		Masked           *bool    `json:"masked"`
		LevelVsLoudestDB *float64 `json:"level_vs_loudest_db"`
		CentroidHz       *float64 `json:"centroid_hz"`
	} `json:"tracks"`
}

// listen runs the loop's step 5 and returns whether each track is masked.
func (q *quickStart) listen() map[string]bool {
	q.t.Helper()
	var report quickAudition
	stdout, code := q.run("audition", "main.cicada", "--json")
	decode(q.t, "audition --json", stdout, &report)
	if code != 0 || report.Schema != "cicada.audition.v1" || len(report.HeardKey) == 0 || report.HarmonyMismatches == nil || len(report.Bars) == 0 || len(report.Tracks) == 0 {
		q.t.Fatalf("audition --json: exit %d %+v", code, report)
	}
	if report.HeardKey[0].Key != report.Score.WrittenKey || *report.HarmonyMismatches != 0 {
		q.t.Fatalf("audition --json: heard %q for %q, %d mismatched bars", report.HeardKey[0].Key, report.Score.WrittenKey, *report.HarmonyMismatches)
	}
	for _, bar := range report.Bars {
		switch bar.Match {
		case "agree", "disagree", "silent", "no-written-pitch":
		default:
			q.t.Fatalf("audition --json: bar match %q", bar.Match)
		}
	}
	masked := map[string]bool{}
	for _, track := range report.Tracks {
		if track.Silent == nil || track.Masked == nil || track.LevelVsLoudestDB == nil || track.CentroidHz == nil || *track.Silent {
			q.t.Fatalf("audition --json: track %+v", track)
		}
		masked[track.ID] = *track.Masked
	}
	return masked
}

// followQuickStart is the start of the agent task suite: it reads the quick
// start from the binary, runs its loop on its own score and batch, and makes
// the three mistakes it describes, each with the refusal it names and the way
// out it gives.
func followQuickStart(t *testing.T, bin string) {
	t.Helper()
	started := time.Now()
	q := &quickStart{t: t, bin: bin, dir: t.TempDir()}

	// The installed binary teaches itself: cicada agents prints the page.
	stdout, code := q.run("agents")
	page, err := os.ReadFile(quickStartPage)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || string(stdout) != string(page) {
		t.Fatalf("cicada agents exited %d and does not print %s; rebuild the binary or run CICADA_UPDATE_AGENT_GUIDE=1 go test ./internal/agentguide", code, quickStartPage)
	}
	q.guide = string(stdout)
	var agents struct {
		Schema     string `json:"schema"`
		Guide      string `json:"guide"`
		GuideBytes int    `json:"guide_bytes"`
		Commands   []struct {
			Command string `json:"command"`
			Summary string `json:"summary"`
		} `json:"commands"`
	}
	stdout, code = q.run("agents", "--json")
	decode(t, "agents --json", stdout, &agents)
	if code != 0 || agents.Schema != "cicada.agents.v1" || agents.Guide != q.guide || agents.GuideBytes != len(q.guide) || len(agents.Commands) == 0 {
		t.Fatalf("agents --json: exit %d, schema %q, %d bytes, %d commands", code, agents.Schema, agents.GuideBytes, len(agents.Commands))
	}
	if agents.GuideBytes > agentguide.QuickStartBudget {
		t.Fatalf("the quick start is %d bytes; its budget is %d", agents.GuideBytes, agentguide.QuickStartBudget)
	}

	// Step 1: start a project.
	q.uses("cicada new demo")
	stdout, code = q.run("new", "demo")
	if code != 0 || string(stdout) != "demo/main.cicada\n" {
		t.Fatalf("new demo: exit %d, printed %q", code, stdout)
	}
	q.dir = filepath.Join(q.dir, "demo")

	// Mistake 1, on the song cicada new writes: a loudness target and no
	// master limiter. The way out is the remedy's text, as replacetext.
	q.uses("cicada render main.cicada -o mix.wav --loudness -14 --json", "loudness_shortfall_at_true_peak_ceiling", "error.remedy.score_text", "remedy.replaces", "replaces", "replacetext", "source", "remedy.revision")
	revision := q.check()
	rendered, code := q.render("--loudness", "-14")
	if code != 1 || rendered.OK || rendered.Error == nil || rendered.Error.Code != "loudness_shortfall_at_true_peak_ceiling" || rendered.Error.Remedy == nil || rendered.Error.Remedy.ScoreText == "" || rendered.Error.Remedy.Revision != revision {
		t.Fatalf("render of the new project to -14 LUFS: exit %d %+v; the quick start's mistake 1 says it fails with loudness_shortfall_at_true_peak_ceiling", code, rendered.Error)
	}
	remedy := rendered.Error.Remedy
	source := string(q.read("main.cicada"))
	if remedy.Replaces != "" {
		source = strings.Replace(source, remedy.Replaces, remedy.ScoreText, 1)
	} else {
		source = strings.TrimRight(source, "\n") + "\n" + remedy.ScoreText + "\n"
	}
	fixed, code := q.apply(map[string]any{"version": 1, "revision": remedy.Revision, "intents": []any{map[string]any{"kind": "replacetext", "source": source}}}, true)
	if code != 0 || !fixed.OK || !fixed.Written {
		t.Fatalf("apply of the remedy: exit %d %+v", code, fixed.Error)
	}
	q.check()
	q.rendersToTarget()

	// Step 2: write the quick start's score.
	q.write("main.cicada", []byte(q.block("<!-- agent-example-file: main.cicada -->")))

	// Step 3: check, and keep the revision.
	q.uses("cicada check --json main.cicada", "valid", "scores[0].revision", "revision")
	revision = q.check()

	// Step 4: render to the target; a bar range without a target reports
	// clipping.
	q.uses("ok", "loudness.target_met", "target_met", "true_peak_max_dbtp", "--loudness", "levels.clipped_count", "--from <bar> --bars <n>")
	q.rendersToTarget()
	part, code := q.render("--from", "2", "--bars", "2")
	if code != 0 || !part.OK || part.Bars != 2 || part.Levels == nil || part.Levels.ClippedCount == nil || *part.Levels.ClippedCount != 0 {
		t.Fatalf("render --from 2 --bars 2: exit %d %+v %+v", code, part, part.Levels)
	}

	// Step 5: listen. The quick start says the bass is masked.
	q.uses("cicada audition main.cicada --json", "tracks[].masked", "level_vs_loudest_db", "tracks[].centroid_hz", "tracks[].silent", "bars", "match", "heard_key[0].key", "score.written_key", "harmony_mismatches")
	if masked := q.listen(); !masked["bass"] || masked["drums"] {
		t.Fatalf("audition: masked tracks %v; the quick start shows the bass masked and the drums not", masked)
	}

	// Step 6: the quick start's batch at our revision; preview, then write.
	q.uses("cicada apply main.cicada edit.json --json", "--write", "written", "diff", "revision_after")
	var envelope map[string]any
	if err := json.Unmarshal([]byte(q.block("<!-- agent-example-file: edit.json -->")), &envelope); err != nil {
		t.Fatalf("the quick start's edit.json: %v", err)
	}
	envelope["revision"] = revision
	before := q.read("main.cicada")
	preview, code := q.apply(envelope, false)
	if code != 0 || !preview.OK || preview.Written || preview.Diff == "" || string(q.read("main.cicada")) != string(before) {
		t.Fatalf("apply preview: exit %d %+v", code, preview)
	}
	applied, code := q.apply(envelope, true)
	if code != 0 || !applied.OK || !applied.Written || applied.RevisionAfter == revision || applied.RevisionAfter != revisionOf(q.read("main.cicada")) {
		t.Fatalf("apply --write: exit %d %+v", code, applied)
	}

	// Mistake 2: the same batch again names the old revision.
	q.uses("revision_mismatch")
	written := q.read("main.cicada")
	stale, code := q.apply(envelope, true)
	if code != 1 || stale.OK || stale.Error == nil || stale.Error.Code != "revision_mismatch" || string(q.read("main.cicada")) != string(written) {
		t.Fatalf("a stale revision: exit %d %+v", code, stale.Error)
	}
	revision = applied.RevisionAfter

	// Listen again: the edit answered the report.
	if masked := q.listen(); masked["bass"] {
		t.Fatal("audition: the bass is still masked after the quick start's edit")
	}

	// Mistake 3: a guessed pattern name, found through the intent lookups.
	q.uses("cicada intents --json --index", "cicada intents --json <kind>", "step:<pattern>/<index>", "intent_refused", "error.index", "error.kind", "error.field", "entity", `unknown pattern "lines"`, "symbols --json")
	var index struct {
		Schema string `json:"schema"`
		Index  []struct {
			Kind    string `json:"kind"`
			Meaning string `json:"meaning"`
		} `json:"index"`
	}
	stdout, code = q.run("intents", "--json", "--index")
	decode(t, "intents --json --index", stdout, &index)
	kinds := map[string]bool{}
	for _, entry := range index.Index {
		kinds[entry.Kind] = entry.Meaning != ""
	}
	if code != 0 || index.Schema != "cicada.intents.v1" || !kinds["togglestep"] || !kinds["setparam"] {
		t.Fatalf("intents --json --index: exit %d, %d kinds", code, len(index.Index))
	}
	var toggle struct {
		Intents []struct {
			Example map[string]any `json:"example"`
		} `json:"intents"`
	}
	stdout, code = q.run("intents", "--json", "togglestep")
	decode(t, "intents --json togglestep", stdout, &toggle)
	if code != 0 || len(toggle.Intents) != 1 || toggle.Intents[0].Example == nil {
		t.Fatalf("intents --json togglestep: exit %d", code)
	}
	guess := toggle.Intents[0].Example
	guess["entity"] = "step:lines/3"
	refused, code := q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{guess}}, true)
	if code != 1 || refused.Error == nil || refused.Error.Code != "intent_refused" || refused.Error.Index == nil || *refused.Error.Index != 0 || refused.Error.Kind != "togglestep" || refused.Error.Field != "entity" || refused.Error.Message != `unknown pattern "lines"` {
		t.Fatalf("a guessed pattern: exit %d %+v", code, refused.Error)
	}
	var symbols []struct {
		Role string `json:"role"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	stdout, code = q.run("symbols", "--json", "main.cicada")
	decode(t, "symbols --json", stdout, &symbols)
	pattern := ""
	for _, symbol := range symbols {
		if symbol.Role == "definition" && symbol.Kind == "pattern" && strings.HasPrefix("lines", symbol.Name) {
			pattern = symbol.Name
		}
	}
	if code != 0 || pattern != "line" {
		t.Fatalf("symbols --json: exit %d, no pattern line in %+v", code, symbols)
	}
	guess["entity"] = "step:" + pattern + "/3"
	accepted, code := q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{guess}}, true)
	if code != 0 || !accepted.OK || !accepted.Written {
		t.Fatalf("the corrected step: exit %d %+v", code, accepted.Error)
	}
	revision = accepted.RevisionAfter

	// Mistake 3 again: a guessed control, and the controls cicada params lists.
	q.uses("param:<track>.<control>", `track keys does not expose sound parameter "cutoff"`, "cicada params", "path", "voices")
	refused, code = q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{map[string]any{"kind": "setparam", "entity": "param:keys.cutoff", "value": "900Hz"}}}, true)
	if code != 1 || refused.Error == nil || refused.Error.Code != "intent_refused" || refused.Error.Message != `track keys does not expose sound parameter "cutoff"` {
		t.Fatalf("a guessed control: exit %d %+v", code, refused.Error)
	}
	var params []struct {
		Path   string   `json:"path"`
		Voices []string `json:"voices"`
	}
	stdout, code = q.run("params")
	decode(t, "params", stdout, &params)
	control := ""
	for _, param := range params {
		for _, voice := range param.Voices {
			if voice == "piano" && param.Path == "cutoff" {
				t.Fatal("cicada params lists cutoff for the piano, which refuses it")
			}
			if voice == "piano" && param.Path == "sustain" {
				control = param.Path
			}
		}
	}
	if code != 0 || control == "" {
		t.Fatalf("cicada params: exit %d, no sustain control for the piano", code)
	}
	accepted, code = q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{map[string]any{"kind": "setparam", "entity": "param:keys." + control, "value": 0.4}}}, true)
	if code != 0 || !accepted.OK || !accepted.Written {
		t.Fatalf("a listed control: exit %d %+v", code, accepted.Error)
	}

	// Every command the quick start offers for looking things up answers.
	filled := strings.NewReplacer("<kind>", "setparam", "<score>", "main.cicada", "<track>.<control>", "bass.cutoff", "<code>", "CICADA-REFERENCE", "<command>", "render")
	for _, lookup := range agents.Commands {
		q.uses(lookup.Command)
		args := strings.Fields(filled.Replace(lookup.Command))
		if args[0] != "cicada" || strings.Contains(strings.Join(args, " "), "<") {
			t.Fatalf("lookup %q has a placeholder the task cannot fill", lookup.Command)
		}
		stdout, code = q.run(args[1:]...)
		var document any
		decodeOK := json.Unmarshal(stdout, &document) == nil
		if code != 0 || (strings.Contains(lookup.Command, "--json") || args[1] == "params") && !decodeOK || len(stdout) == 0 {
			t.Fatalf("lookup %q: exit %d\n%s", lookup.Command, code, stdout)
		}
	}
	t.Logf("quick start task: %d commands in %s", q.commands, time.Since(started).Round(time.Millisecond))
}
