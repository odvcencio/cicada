package agentsuite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/agentguide"
)

// quickStartPage is the page cicada agents prints.
const quickStartPage = "../../docs/manual/agent-quickstart.md"

// quickStart drives the binary as a new agent does: it knows only the text
// cicada agents printed. Every command, field and refusal the task relies on
// must be named in that text (see uses), and every claim the text makes is
// tried against the binary (quickstart_claims_test.go), so the page can
// neither stop teaching what the loop needs nor say what the binary does not
// do.
type quickStart struct {
	t        *testing.T
	bin      string
	guide    string
	root     string // the empty folder the task starts in
	dir      string // the folder commands run in
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

// says fails unless the quick start contains text as written.
func (q *quickStart) says(text string) {
	q.t.Helper()
	if !strings.Contains(q.guide, text) {
		q.t.Errorf("the quick start does not say %q", text)
	}
}

func (q *quickStart) run(args ...string) ([]byte, int) {
	q.t.Helper()
	q.commands++
	return cicada(q.t, q.bin, q.dir, args...)
}

// in runs fn with commands running in dir, then returns to the project.
func (q *quickStart) in(dir string, fn func()) {
	q.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		q.t.Fatal(err)
	}
	saved := q.dir
	q.dir = dir
	defer func() { q.dir = saved }()
	fn()
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

// line returns the quick start's text from the line that starts with prefix
// to the next blank line or list item: one paragraph or bullet.
func (q *quickStart) line(prefix string) string {
	q.t.Helper()
	for _, paragraph := range regexp.MustCompile(`\n(?:\s*\n|- |[0-9]\. )`).Split(q.guide, -1) {
		if strings.HasPrefix(strings.TrimSpace(paragraph), prefix) || strings.HasPrefix(paragraph, prefix) {
			return paragraph
		}
	}
	q.t.Fatalf("the quick start has no paragraph starting %q", prefix)
	return ""
}

func revisionOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type quickCheck struct {
	Schema string `json:"schema"`
	Valid  bool   `json:"valid"`
	Scores []struct {
		File        string `json:"file"`
		Revision    string `json:"revision"`
		Valid       bool   `json:"valid"`
		Diagnostics []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Start   *struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
			Hint string `json:"hint"`
			Fix  *struct {
				Intents  []map[string]any `json:"intents"`
				Revision string           `json:"revision"`
			} `json:"fix"`
		} `json:"diagnostics"`
	} `json:"scores"`
}

// checkScore checks one score and returns the document and the exit status.
func (q *quickStart) checkScore(score string) (quickCheck, int) {
	q.t.Helper()
	var check quickCheck
	stdout, code := q.run("check", "--json", score)
	decode(q.t, "check --json", stdout, &check)
	if check.Schema != "cicada.check.v1" || len(check.Scores) != 1 {
		q.t.Fatalf("check --json %s: exit %d %+v", score, code, check)
	}
	return check, code
}

// browserProfile checks main.cicada with --profile browser and requires it
// to pass, or to fail only because the browser image cannot carry the master
// insert.
func (q *quickStart) browserProfile(pass bool) {
	q.t.Helper()
	var check quickCheck
	stdout, code := q.run("check", "--profile", "browser", "--json", "main.cicada")
	decode(q.t, "check --profile browser --json", stdout, &check)
	if pass {
		if code != 0 || !check.Valid {
			q.t.Fatalf("check --profile browser: exit %d %+v", code, check)
		}
		return
	}
	if code != 1 || check.Valid || len(check.Scores) != 1 || len(check.Scores[0].Diagnostics) != 1 || !strings.Contains(check.Scores[0].Diagnostics[0].Message, "master processor") {
		q.t.Fatalf("check --profile browser with a master limiter: exit %d %+v; the quick start says only the master insert is refused", code, check)
	}
}

// check runs the loop's step 3 and returns the score's revision.
func (q *quickStart) check() string {
	q.t.Helper()
	check, code := q.checkScore("main.cicada")
	if code != 0 || !check.Valid || !check.Scores[0].Valid {
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
		Reason  string `json:"reason"`
	} `json:"error"`
}

// applyTo writes envelope to edit.json and applies it to score, writing when
// write is set.
func (q *quickStart) applyTo(score string, envelope any, write bool) (quickApply, int) {
	q.t.Helper()
	data, err := json.Marshal(envelope)
	if err != nil {
		q.t.Fatal(err)
	}
	q.write("edit.json", data)
	args := []string{"apply", score, "edit.json", "--json"}
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

func (q *quickStart) apply(envelope any, write bool) (quickApply, int) {
	q.t.Helper()
	return q.applyTo("main.cicada", envelope, write)
}

// edit applies intents to main.cicada at revision, writing, and fails unless
// the batch is written. It returns the new revision.
func (q *quickStart) edit(what, revision string, intents ...any) string {
	q.t.Helper()
	result, code := q.apply(map[string]any{"version": 1, "revision": revision, "intents": intents}, true)
	if code != 0 || !result.OK || !result.Written {
		q.t.Fatalf("%s: exit %d %+v", what, code, result.Error)
	}
	return result.RevisionAfter
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
			Revision string           `json:"revision"`
			Intents  []map[string]any `json:"intents"`
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

// rendersToTarget renders to -14 LUFS, requires the target met, checks what
// the quick start says target_met means, and returns the loudness and true
// peak reached.
func (q *quickStart) rendersToTarget() (float64, float64) {
	q.t.Helper()
	rendered, code := q.render("--loudness", "-14")
	loudness := rendered.Loudness
	if code != 0 || !rendered.OK || loudness == nil || !loudness.TargetMet || len(rendered.Outputs) != 1 || rendered.Outputs[0].Path != "mix.wav" {
		q.t.Fatalf("render --loudness -14: exit %d %+v %+v", code, rendered, loudness)
	}
	if math.Abs(*loudness.IntegratedLUFS-loudness.TargetLUFS) > loudness.ToleranceLU || *loudness.TruePeakDBTP > loudness.TruePeakMaxDBTP {
		q.t.Fatalf("target_met is true at %.2f LUFS and %.2f dBTP", *loudness.IntegratedLUFS, *loudness.TruePeakDBTP)
	}
	return *loudness.IntegratedLUFS, *loudness.TruePeakDBTP
}

type quickAudition struct {
	Schema string `json:"schema"`
	Score  struct {
		WrittenKey string `json:"written_key"`
		FromBar    int    `json:"from_bar"`
		Bars       int    `json:"bars"`
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

// audition runs audition --json with args and checks the fields the quick
// start reads.
func (q *quickStart) audition(args ...string) quickAudition {
	q.t.Helper()
	var report quickAudition
	stdout, code := q.run(append([]string{"audition", "main.cicada"}, append(args, "--json")...)...)
	decode(q.t, "audition --json", stdout, &report)
	if code != 0 || report.Schema != "cicada.audition.v1" || len(report.HeardKey) == 0 || report.HarmonyMismatches == nil || len(report.Bars) == 0 || len(report.Tracks) == 0 {
		q.t.Fatalf("audition --json: exit %d %+v", code, report)
	}
	for _, bar := range report.Bars {
		switch bar.Match {
		case "agree", "disagree", "silent", "no-written-pitch":
		default:
			q.t.Fatalf("audition --json: bar match %q", bar.Match)
		}
	}
	for _, track := range report.Tracks {
		if track.Silent == nil || track.Masked == nil || track.LevelVsLoudestDB == nil || track.CentroidHz == nil {
			q.t.Fatalf("audition --json: track %+v", track)
		}
	}
	return report
}

// heardTrack is what the report says of one track.
type heardTrack struct {
	masked bool
	level  float64 // level_vs_loudest_db
}

// listen runs the loop's step 5 and returns the report's tracks by id.
func (q *quickStart) listen() map[string]heardTrack {
	q.t.Helper()
	report := q.audition()
	if report.HeardKey[0].Key != report.Score.WrittenKey || *report.HarmonyMismatches != 0 {
		q.t.Fatalf("audition --json: heard %q for %q, %d mismatched bars", report.HeardKey[0].Key, report.Score.WrittenKey, *report.HarmonyMismatches)
	}
	tracks := map[string]heardTrack{}
	for _, track := range report.Tracks {
		if *track.Silent {
			q.t.Fatalf("audition --json: track %s is silent", track.ID)
		}
		tracks[track.ID] = heardTrack{masked: *track.Masked, level: *track.LevelVsLoudestDB}
	}
	return tracks
}

// followQuickStart is the start of the agent task suite: it reads the quick
// start from the binary, runs its loop on its own score and batch, makes the
// three mistakes it describes, each with the refusal it names and the way out
// it gives, and tries every other claim it makes.
func followQuickStart(t *testing.T, bin string) {
	t.Helper()
	started := time.Now()
	q := &quickStart{t: t, bin: bin, root: t.TempDir()}
	q.dir = q.root

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

	// Step 1: start a project. Its song passes check on both profiles.
	q.uses("cicada new demo", "main.cicada", "check --profile browser")
	stdout, code = q.run("new", "demo")
	if code != 0 || string(stdout) != "demo/main.cicada\n" {
		t.Fatalf("new demo: exit %d, printed %q", code, stdout)
	}
	q.dir = filepath.Join(q.root, "demo")
	// cicada new writes a short AGENTS.md that points to cicada agents, and
	// cicada agents reference prints the core reference.
	if written, err := os.ReadFile(filepath.Join(q.dir, "AGENTS.md")); err != nil || string(written) != agentguide.ProjectAgents() || !strings.Contains(string(written), "`cicada agents`") {
		t.Fatalf("the new project's AGENTS.md does not point to cicada agents: %v", err)
	}
	var created struct {
		Schema          string   `json:"schema"`
		Files           []string `json:"files"`
		AgentsMDWritten bool     `json:"agents_md_written"`
		Error           *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	stdout, code = cicada(t, bin, q.root, "new", "--json", "second")
	decode(t, "new --json", stdout, &created)
	if code != 0 || created.Schema != "cicada.new.v1" || !created.AgentsMDWritten || !strings.Contains(strings.Join(created.Files, " "), "second/AGENTS.md") {
		t.Fatalf("new --json: exit %d %+v", code, created)
	}
	created.Error = nil
	stdout, code = cicada(t, bin, q.root, "new", "--json", "second")
	decode(t, "new --json again", stdout, &created)
	if code != 1 || created.Error == nil || created.Error.Code != "destination_exists" {
		t.Fatalf("new --json into an existing folder: exit %d %+v", code, created.Error)
	}
	stdout, code = q.run("agents", "reference")
	if reference := string(stdout); code != 0 || !strings.HasPrefix(reference, "# Cicada core reference for agents\n") || !strings.Contains(reference, "\n## Edit intents\n") || len(reference) > agentguide.ReferenceBudget {
		t.Fatalf("agents reference: exit %d, %d bytes", code, len(stdout))
	}
	revision := q.check()
	q.browserProfile(true)

	// Mistake 1, on that song: it has no master limiter, so -14 LUFS fails at
	// the true-peak ceiling. A missed target still writes its best try.
	q.uses("loudness_shortfall_at_true_peak_ceiling", "error.remedy.intents", "remedy.revision", "--write", "loudness.integrated_lufs")
	rendered, code := q.render("--loudness", "-14")
	if code != 1 || rendered.OK || rendered.Error == nil || rendered.Error.Code != "loudness_shortfall_at_true_peak_ceiling" || rendered.Error.Remedy == nil || len(rendered.Error.Remedy.Intents) == 0 || rendered.Error.Remedy.Revision != revision || rendered.Loudness == nil || rendered.Loudness.IntegratedLUFS == nil {
		t.Fatalf("render of the new song to -14 LUFS: exit %d %+v; the quick start's mistake 1 says it fails with loudness_shortfall_at_true_peak_ceiling and a remedy with intents", code, rendered.Error)
	}
	if _, err := os.Stat(filepath.Join(q.dir, "mix.wav")); err != nil || len(rendered.Outputs) != 1 {
		t.Fatalf("a missed target wrote no best try: %v", err)
	}
	// For the browser: a target at or below the loudness it reached is met
	// with no limiter, and the project still passes the browser profile.
	reached := strconv.FormatFloat(math.Floor(*rendered.Loudness.IntegratedLUFS*10)/10, 'f', 1, 64)
	if aimed, code := q.render("--loudness", reached); code != 0 || !aimed.OK || !aimed.Loudness.TargetMet {
		t.Fatalf("render of the new song at the reached %s LUFS: exit %d %+v", reached, code, aimed.Error)
	}
	q.browserProfile(true)
	// The way out: the remedy's intents add the limiter; -14 LUFS is then met,
	// and the browser profile refuses the master insert.
	remedy := rendered.Error.Remedy
	q.edit("the remedy's intents", remedy.Revision, anySlice(remedy.Intents)...)
	q.check()
	lufs, peak := q.rendersToTarget()
	t.Logf("the song cicada new writes, with the remedy's limiter, renders to %.2f LUFS at %.2f dBTP; without it, %s LUFS", lufs, peak, reached)
	q.browserProfile(false)

	// Step 2: write the quick start's score.
	q.write("main.cicada", []byte(q.block("<!-- agent-example-file: main.cicada -->")))

	// Step 3: check, and keep the revision.
	q.uses("cicada check --json main.cicada", "valid", "scores[0].revision", "revision")
	revision = q.check()

	// Step 4: render to the target; a bar range without a target reports
	// clipping.
	q.uses("cicada render main.cicada -o mix.wav --loudness -14 --json", "ok", "loudness.target_met", "target_met", "true_peak_max_dbtp", "--loudness", "levels.clipped_count", "--from <bar> --bars <n>")
	q.rendersToTarget()
	part, code := q.render("--from", "2", "--bars", "2")
	if code != 0 || !part.OK || part.Bars != 2 || part.Levels == nil || part.Levels.ClippedCount == nil || *part.Levels.ClippedCount != 0 {
		t.Fatalf("render --from 2 --bars 2: exit %d %+v %+v", code, part, part.Levels)
	}
	if report := q.audition("--from", "5", "--bars", "2"); report.Score.FromBar != 5 || report.Score.Bars != 2 || len(report.Bars) != 2 {
		t.Fatalf("audition --from 5 --bars 2: %+v", report.Score)
	}

	// Step 5: listen. The quick start says the bass is masked, and how far
	// under the drums it is, to one decimal.
	q.uses("cicada audition main.cicada --json", "tracks[].masked", "level_vs_loudest_db", "tracks[].centroid_hz", "tracks[].silent", "bars", "match", "heard_key[0].key", "score.written_key", "harmony_mismatches", "disagree", "silent")
	heard := q.listen()
	if !heard["bass"].masked || heard["drums"].masked {
		t.Fatalf("audition: %+v; the quick start shows the bass masked and the drums not", heard)
	}
	q.says(fmt.Sprintf("the bass is %.1f dB under the drums", -heard["bass"].level))

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
	q.uses("revision_mismatch", "check --json")
	written := q.read("main.cicada")
	stale, code := q.apply(envelope, true)
	if code != 1 || stale.OK || stale.Error == nil || stale.Error.Code != "revision_mismatch" || string(q.read("main.cicada")) != string(written) {
		t.Fatalf("a stale revision: exit %d %+v", code, stale.Error)
	}
	revision = applied.RevisionAfter
	if again := q.check(); again != revision {
		t.Fatalf("check --json after the write: revision %s, revision_after %s", again, revision)
	}

	// Listen again: the edit answered the report.
	if q.listen()["bass"].masked {
		t.Fatal("audition: the bass is still masked after the quick start's edit")
	}

	// The claims the page makes beside the loop.
	revision = q.envelopeClaims(revision)
	revision = q.mistakeThree(revision)
	q.refusalClaims(revision)
	q.scoreClaims()
	q.voiceClaims()

	// Every command the quick start offers for looking things up answers.
	filled := strings.NewReplacer("<kind>", "setparam", "<score>", "main.cicada", "<track>.<control>", "bass.cutoff", "<code>", "CICADA-REFERENCE", "<command>", "render", "<voice>", "acid")
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

func anySlice(values []map[string]any) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
