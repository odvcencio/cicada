package agentguide

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/diagnostics"
)

// The agent front door, split by how often its inputs change. Many pull
// requests are open at once and most touch the manual or the catalogs, so a
// committed file that copied them would conflict in nearly every one:
//
//   - Committed (Build): files whose only inputs are the quick start and the
//     stable text and page table below. TestAgentFrontDoorIsCurrent fails
//     while one is stale.
//   - Built by the binary when asked (BuildReference): the core reference,
//     from the binary's own catalogs, so it is current by construction.
//   - Built on demand (BuildFull): llms-full.txt, from the manual pages, by
//     make agent-front-door and as a CI artifact; never committed.

// FrontDoorCommand rewrites the committed files and llms-full.txt.
const FrontDoorCommand = "make agent-front-door"

// The budgets, in bytes (about four bytes a token).
const (
	ReferenceBudget = 48000  // the core reference: about 12,000 tokens
	LLMsBudget      = 6000   // llms.txt
	LLMsFullBudget  = 240000 // llms-full.txt: about 60,000 tokens
	AgentsMDBudget  = 6000   // the repository's AGENTS.md, generated region included
)

// Command is one line of the command table, which package main holds.
type Command struct {
	Name, Summary string
}

// Inputs are what Build reads: the repository root.
type Inputs struct {
	Root string
}

// The region of the repository's AGENTS.md that Build writes; the rest of the
// file is written by hand.
const (
	regionBegin = "<!-- agent-front-door:begin -->"
	regionEnd   = "<!-- agent-front-door:end -->"
)

// llmsPage is one page llms.txt lists: its path, title and note, written here
// rather than copied, so a change to the page does not change llms.txt.
type llmsPage struct{ path, title, note string }

// llmsSections are the sections of llms.txt. Every page of the manual index
// (docs/manual/README.md) must be in one, or Build fails; that is the only
// thing Build reads from the index. llms-full.txt holds the full text of the
// pages outside Optional.
var llmsSections = []struct {
	name  string
	pages []llmsPage
}{
	{"Start here", []llmsPage{
		{"docs/manual/agent-quickstart.md", "Cicada quick start for agents", "the page an agent reads first, which `cicada agents` prints"},
		{"docs/manual/agents.md", "Agents and JSON output", "every `--json` format, its fields and error codes"},
	}},
	{"Language", []llmsPage{
		{"docs/manual/writing-music.md", "Writing music", "tracks, patterns, pitches, drum rows, scenes and songs"},
		{"docs/manual/composing-songs.md", "Composing a complete song", "form, chord voicing, grids and transitions"},
		{"docs/spec/edition-2.md", "Cicada source edition 2", "what edition 2 of the language adds and changes"},
		{"docs/spec/features.md", "Language extensions", "every feature of the language, with its rules"},
	}},
	{"Sound and delivery", []llmsPage{
		{"docs/manual/exporting.md", "Exporting", "WAV, stems and MIDI"},
		{"docs/manual/audition.md", "Audition", "the listening report: key, chords, levels, brightness, rhythm"},
		{"docs/audio/mix-chain.md", "Opt-in mix chain", "track inserts, sends, master effects and loudness targets"},
		{"docs/manual/parameter-ranges.md", "Parameter ranges", "the allowed range of every voice, drum and effect setting"},
	}},
	{"Reference", []llmsPage{
		{"docs/manual/diagnostics.md", "Diagnostic codes", "every diagnostic code with its meaning and fix"},
		{"docs/manual/troubleshooting.md", "Troubleshooting", "notation, editor, playback and export problems"},
		{"docs/cli.md", "CLI reference", "every command and flag of the cicada binary"},
	}},
	{"Optional", []llmsPage{
		{"docs/manual/macos.md", "Cicada Studio on Mac", "the native app, files, audio devices and shortcuts"},
		{"docs/manual/studio.md", "Cicada Studio", "the editor: source, grids, scenes, arrangement and history"},
		{"docs/manual/modeled-percussion.md", "Modeled percussion", "struck bars, drums and plates"},
		{"docs/manual/sampler.md", "Pad sampler", "chop samples, play pads, record and resample"},
		{"docs/manual/editors.md", "Editors and notation tools", "the language server, editor extension, formatter and fixes"},
		{"docs/manual/libraries.md", "Managing libraries", "create, inspect, pin and vendor reusable declarations and audio"},
		{"docs/manual/next-level.md", "Live playback and mixing", "native audio, host macros, MIDI and mixer settings"},
		{"docs/manual/modulation.md", "Modulation", "modulation sources and their routes"},
		{"docs/manual/engine-host.md", "Hosting the engine", "the Go and TinyGo audio engine in another program"},
		{"docs/manual/engine-metrics.md", "Engine measurements", "CPU and render costs of synthetic sessions"},
		{"docs/manual/audition-measures.md", "Audition measures", "how audition measures loudness, band owners, activity and peaks; its limits and cost"},
	}},
}

// frontDoorInputs are the only repository files Build reads: the quick start,
// AGENTS.md (whose hand-written part it keeps) and the manual index (only to
// check that every page has a place in llms.txt).
// TestFrontDoorReadsOnlyStableInputs builds the front door from a copy of
// these files alone and requires the same bytes.
var frontDoorInputs = []string{"docs/manual/agent-quickstart.md", "AGENTS.md", "docs/manual/README.md"}

// Build returns the committed files of the agent front door, by path from the
// repository root. Their inputs are frontDoorInputs and the stable text in
// this package; no catalog of the binary reaches them.
func Build(in Inputs) (map[string][]byte, error) {
	text := make(map[string]string, len(frontDoorInputs))
	for _, input := range frontDoorInputs {
		data, err := os.ReadFile(filepath.Join(in.Root, filepath.FromSlash(input)))
		if err != nil {
			return nil, err
		}
		text[input] = string(data)
	}
	quickstart := text["docs/manual/agent-quickstart.md"]
	agents, err := replaceRegion(text["AGENTS.md"], frontDoorRegion(quickstart))
	if err != nil {
		return nil, fmt.Errorf("AGENTS.md: %w", err)
	}
	index := text["docs/manual/README.md"]
	if err := everyIndexedPagePlaced(index); err != nil {
		return nil, err
	}
	return map[string][]byte{
		"internal/agentguide/quickstart.md": []byte(quickstart),
		"AGENTS.md":                         []byte(agents),
		"llms.txt":                          []byte(buildLLMs()),
		"examples/first-song/AGENTS.md":     []byte(ProjectAgents()),
	}, nil
}

// ProjectAgents is the AGENTS.md cicada new writes into a project: what the
// project is, where the current guide comes from, and the rules that do not
// change. It copies nothing that goes stale as Cicada changes.
func ProjectAgents() string {
	return `# AGENTS.md

This folder is a Cicada project: ` + "`main.cicada`" + ` is the song, written as a text score,
and ` + "`cicada.mod`" + ` makes the folder a project in edition 2 of the language.

- ` + "`cicada agents`" + ` prints the quick start for the installed ` + "`cicada`" + `, and
  ` + "`cicada agents reference`" + ` the reference: every command, edit intent and
  diagnostic code.
- Check after every edit: ` + "`cicada check --json main.cicada`" + `, until ` + "`valid`" + ` is
  true.
- Change the score one revision-checked batch at a time:
  ` + "`cicada apply main.cicada edit.json --write --json`" + `, with the ` + "`revision`" + ` that
  the last ` + "`check --json`" + ` or ` + "`apply`" + ` gave. A batch whose revision is stale is
  refused and changes nothing.
`
}

// ReferenceInputs are what the binary passes to BuildReference: the parts of
// the command line that package main holds.
type ReferenceInputs struct {
	Commands    []Command
	ApplyCodes  []string // the error codes of apply --json
	RenderCodes []string // the error codes of render --json
}

// referenceConventions is the reference's summary of the JSON conventions;
// docs/manual/agents.md has them in full.
const referenceConventions = `- With ` + "`--json`" + ` a command prints one JSON document on standard output, on
  success and on failure; its ` + "`schema`" + ` names the format and version, such as
  ` + "`cicada.check.v1`" + `. Within a version fields are only added.
- A failure has ` + "`error`" + ` with a stable ` + "`code`" + ` and a ` + "`message`" + `; the exit status is
  the same as without ` + "`--json`" + `.
- Keys are snake_case; a path inside the working folder is relative to it.
`

// BuildReference returns the core reference the binary prints with cicada
// agents reference: the quick start, the JSON conventions in brief, and one
// line for every command, edit intent, diagnostic code and effect route of
// this binary. Nothing in it is committed, so it is current by construction.
func BuildReference(in ReferenceInputs) string {
	var page strings.Builder
	page.WriteString("# Cicada core reference for agents\n\n")
	page.WriteString("The quick start, the JSON conventions in brief, then one line for every\n")
	page.WriteString("command, edit intent, diagnostic code and effect route of this binary, which\n")
	page.WriteString("builds this page from its own catalogs. `docs/manual/agents.md` describes every\n")
	page.WriteString("format in full.\n\n")
	page.WriteString("## Quick start\n\n" + withoutTitle(exampleMarkers.ReplaceAllString(QuickStart, "")) + "\n")
	page.WriteString("## JSON conventions\n\n" + referenceConventions + "\n")
	fmt.Fprintf(&page, "Error codes of `apply --json`: %s.\n\n", codeList(in.ApplyCodes))
	fmt.Fprintf(&page, "Error codes of `render --json`: %s.\n\n", codeList(in.RenderCodes))
	page.WriteString("## Commands\n\n")
	for _, command := range in.Commands {
		fmt.Fprintf(&page, "- `cicada %s`: %s\n", command.Name, command.Summary)
	}
	page.WriteString("\n## Edit intents\n\n`cicada intents --json <kind>` gives a kind's fields and examples.\n\n")
	for _, info := range edits.Catalog() {
		fmt.Fprintf(&page, "- `%s`: %s\n", info.Kind, oneLine(info.Meaning))
	}
	page.WriteString("\n## Diagnostic codes\n\n`cicada diagnostics --json <code>` gives a code's fix.\n\n")
	for _, code := range diagnostics.Catalog() {
		fmt.Fprintf(&page, "- `%s` (%s): %s\n", code.Code, strings.Join(code.Severities, ", "), oneLine(code.Meaning))
	}
	page.WriteString("\n## Effect routes\n\nWhere `addeffect` may put each kind. A send return takes no `inserton`;\nraise `param:<track>.send.<name>` instead.\n\n")
	page.WriteString("| Kind | Inserted on | Send return | Only in an insert chain |\n| --- | --- | --- | --- |\n")
	for _, route := range edits.EffectRoutes() {
		on := strings.Join(route.InsertOn, ", ")
		if on == "" {
			on = "none"
		}
		fmt.Fprintf(&page, "| `%s` | %s | %s | %s |\n", route.Kind, on, yes(route.Return), yes(route.InsertsOnly))
	}
	return page.String()
}

func codeList(codes []string) string {
	quoted := make([]string, len(codes))
	for i, code := range codes {
		quoted[i] = "`" + code + "`"
	}
	return strings.Join(quoted, ", ")
}

func frontDoorRegion(quickstart string) string {
	loop, err := section(quickstart, "## The loop")
	if err != nil {
		loop = ""
	}
	loop = strings.TrimSpace(strings.TrimPrefix(loop, "## The loop"))
	if before, _, found := strings.Cut(loop, "\n\n"); found && strings.HasPrefix(loop, "1.") {
		loop = before
	}
	loop = strings.ReplaceAll(loop, "(next section)", "(the quick start's next section)")
	var region strings.Builder
	region.WriteString(regionBegin + "\n")
	region.WriteString("## Using Cicada to make music\n\n")
	region.WriteString("<!-- Generated from docs/manual/agent-quickstart.md; run " + FrontDoorCommand + " -->\n\n")
	region.WriteString("An agent that makes music with an installed `cicada` binary starts with\n")
	region.WriteString("`cicada agents`, which prints the quick start\n")
	region.WriteString("([docs/manual/agent-quickstart.md](docs/manual/agent-quickstart.md)). Its loop:\n\n")
	region.WriteString(loop + "\n\n")
	region.WriteString("`cicada agents reference` prints the core reference, which the binary builds\n")
	region.WriteString("from its own catalogs, and [llms.txt](llms.txt) lists the manual for agents.\n")
	region.WriteString(regionEnd)
	return region.String()
}

func replaceRegion(file, region string) (string, error) {
	start := strings.Index(file, regionBegin)
	end := strings.Index(file, regionEnd)
	if start < 0 || end < start {
		return "", fmt.Errorf("no %s ... %s region", regionBegin, regionEnd)
	}
	return file[:start] + region + file[end+len(regionEnd):], nil
}

func buildLLMs() string {
	var llms strings.Builder
	llms.WriteString("# Cicada\n\n")
	llms.WriteString("> Cicada is a music workstation in which a song is a readable text score. The\n")
	llms.WriteString("> same score renders to the same audio on every machine and in the browser.\n\n")
	llms.WriteString("An agent starts with `cicada agents`, which prints the quick start: write a\n")
	llms.WriteString("score, `cicada check --json`, `cicada render --json`, `cicada audition --json`,\n")
	llms.WriteString("then edit it with `cicada apply`. `cicada agents reference` prints the core\n")
	llms.WriteString("reference from the binary, and `make agent-front-door` builds llms-full.txt,\n")
	llms.WriteString("the full text of the pages outside Optional.\n")
	for _, section := range llmsSections {
		fmt.Fprintf(&llms, "\n## %s\n\n", section.name)
		for _, page := range section.pages {
			fmt.Fprintf(&llms, "- [%s](%s): %s\n", page.title, page.path, page.note)
		}
	}
	return llms.String()
}

var indexEntry = regexp.MustCompile(`(?m)^- \[[^\]]+\]\(([^)]+)\) — `)

// everyIndexedPagePlaced fails when a page of the manual index is in no
// section of llms.txt.
func everyIndexedPagePlaced(index string) error {
	placed := map[string]bool{}
	for _, section := range llmsSections {
		for _, page := range section.pages {
			placed[page.path] = true
		}
	}
	for _, match := range indexEntry.FindAllStringSubmatch(index, -1) {
		if page := "docs/manual/" + match[1]; !placed[page] {
			return fmt.Errorf("llms.txt: %s is in the manual index but in no section; add it to llmsSections", page)
		}
	}
	return nil
}

// BuildFull returns llms-full.txt: the quick start and the full text of every
// page llms.txt lists outside Optional, read from the repository at root.
func BuildFull(root string) ([]byte, error) {
	var full strings.Builder
	full.WriteString("# Cicada: the manual for agents\n\n")
	full.WriteString("<!-- Built by " + FrontDoorCommand + "; not committed. -->\n\n")
	full.WriteString("The full text of the manual pages that llms.txt lists outside Optional.\n")
	full.WriteString("Links are relative to the repository root.\n")
	for _, section := range llmsSections {
		if section.name == "Optional" {
			continue
		}
		for _, page := range section.pages {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page.path)))
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&full, "\n# %s\n\n%s", page.path, rebaseLinks(string(data), path.Dir(page.path), "."))
		}
	}
	return []byte(full.String()), nil
}

// section returns the text of a page from the heading line to the next
// heading of the same or a higher level, outside fenced blocks. For a page's
// title (level 1) it is the page's opening, up to its first section.
func section(text, heading string) (string, error) {
	level := max(strings.Count(strings.Fields(heading)[0], "#"), 2)
	lines := strings.Split(text, "\n")
	start := -1
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		}
		if fenced {
			continue
		}
		if start < 0 {
			if line == heading {
				start = i
			}
			continue
		}
		if hashes := len(line) - len(strings.TrimLeft(line, "#")); hashes > 0 && hashes <= level && strings.HasPrefix(line[hashes:], " ") {
			return strings.TrimRight(strings.Join(lines[start:i], "\n"), "\n") + "\n", nil
		}
	}
	if start < 0 {
		return "", fmt.Errorf("no heading %q", heading)
	}
	return strings.TrimRight(strings.Join(lines[start:], "\n"), "\n") + "\n", nil
}

// demote adds one level to every heading outside fenced blocks.
func demote(text string) string {
	lines := strings.Split(text, "\n")
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(line, "#") {
			lines[i] = "#" + line
		}
	}
	return strings.Join(lines, "\n")
}

// withoutTitle drops a page's first heading line and the blank lines after it.
func withoutTitle(text string) string {
	if strings.HasPrefix(text, "#") {
		_, text, _ = strings.Cut(text, "\n")
	}
	return demote(strings.TrimLeft(text, "\n"))
}

var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// exampleMarkers are the lines of the quick start that the documentation
// harness reads (agent-example markers and the blank line after each); the
// reference leaves them out.
var exampleMarkers = regexp.MustCompile(`(?m)^<!-- agent-example[^>]*-->\n\n?`)

// rebaseLinks rewrites relative links of text, written for a page in from,
// so they resolve from the folder to (both relative to the repository root).
func rebaseLinks(text, from, to string) string {
	return markdownLink.ReplaceAllStringFunc(text, func(link string) string {
		target := link[2 : len(link)-1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "mailto:") {
			return link
		}
		file, anchor, _ := strings.Cut(target, "#")
		rebased := relative(to, path.Join(from, file))
		if anchor != "" {
			rebased += "#" + anchor
		}
		return "](" + rebased + ")"
	})
}

// relative returns the slash path of target relative to the folder dir.
func relative(dir, target string) string {
	dirParts := strings.Split(path.Clean(dir), "/")
	targetParts := strings.Split(path.Clean(target), "/")
	if dirParts[0] == "." {
		dirParts = nil
	}
	common := 0
	for common < len(dirParts) && common < len(targetParts)-1 && dirParts[common] == targetParts[common] {
		common++
	}
	var parts []string
	for range dirParts[common:] {
		parts = append(parts, "..")
	}
	return strings.Join(append(parts, targetParts[common:]...), "/")
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
