package main

import (
	"fmt"
	"io"
	"strings"
)

type commandHelpEntry struct {
	name, summary, usage, flags, notes string
}

var commandHelpEntries = []commandHelpEntry{
	{name: "new", summary: "create an edition-2 project", usage: "cicada new [--json] <name>", flags: "  --json      write what was created as JSON (schema cicada.new.v1)\n  --help      show this help", notes: "Writes main.cicada (a working song), cicada.mod, .gitignore and a short AGENTS.md that points an agent to cicada agents; an AGENTS.md already there is never replaced. With --json, agents_md_written says whether it wrote one."},
	{name: "agents", summary: "print the quick start for AI agents", usage: "cicada agents [reference] [--json]", flags: "  --json      write the page as JSON (schema cicada.agents.v1) with its size in bytes, the commands that look up the rest and the pages the binary carries\n  --help      show this help", notes: "The quick start teaches the loop (new, check, render, audition, apply), enough of the score language for a song with drums, bass and chords, the edit envelope, how to read a refusal and where to look things up. It is docs/manual/agent-quickstart.md, built into the binary, so it needs no repository checkout. cicada agents reference prints the core reference, which the binary builds from its own catalogs: the quick start, the JSON conventions in brief, and one line for every command, edit intent, diagnostic code and effect route."},
	{name: "mcp", summary: "serve the read-only tools to an MCP client over stdio", usage: "cicada mcp [--root <dir>] [--allow-user-library]", flags: "  --root <dir>          the project folder the tools read (default: the working folder)\n  --allow-user-library  also read the user library outside the root (default: off)\n  --help                show this help", notes: "Speaks the Model Context Protocol on standard input and output, one JSON-RPC message per line: revision 2026-07-28 (stateless, server/discover) and, after an initialize request, 2025-11-25 or 2025-06-18. The tools song, check, intents, explain, library and params return the documents of song --json, check --json, intents --json, diagnostics --json, explain --json, lib --json and params; the resources cicada://guide/quickstart and cicada://guide/reference are what cicada agents and cicada agents reference print. A result over 256 KiB is refused with too_large. The root must be a project's own folder, not a folder inside one or a library. Nothing is written but an empty temporary folder that stands in for the user library, and no connection is opened. See docs/manual/agents.md."},
	{name: "bundle", summary: "package a portable score with provenance", usage: "cicada bundle <score.cicada> <bundle.zip>\n  cicada bundle --restore-history <extracted-score.cicada>", flags: "  --restore-history  restore inventoried recovery versions after extraction", notes: "Copies score sources, referenced audio, pinned libraries, and recovery versions into a ZIP with a SHA-256 file inventory. Existing output files are refused. Restore preserves the extracted originals and refuses conflicting recovery files."},
	{name: "apply", summary: "apply typed edit intents to a score", usage: "cicada apply <score.cicada> <intents.json> [--write] [--json] [--author <name>] [--session <id>]", flags: "  --write         write the validated edits (default: print a diff only)\n  --json          write the result as JSON (schema cicada.apply.v1): revisions, diff, each intent's label, or the error with the failing intent's index, kind and field\n  --author <name>  commit author (default: envelope author or cli)\n  --session <id>   commit session\n  --cpuprofile <file>  write a Go CPU profile of the command to file\n  --help          show this help", notes: "A supplied revision must match the score. Writes also check for concurrent saves. An envelope with dryrun:true never writes. An error names the failing intent as intents[<index>].<field> (<kind>); run cicada intents for every kind and its fields."},
	{name: "intents", summary: "list the edit intents apply accepts", usage: "cicada intents [--json] [--index | kind]", flags: "  --json      write the catalog as JSON (schema cicada.intents.v1)\n  --index     with --json, list only each kind and its meaning (schema cicada.intents_index.v1)\n  --help      show this help", notes: "Lists every intent kind with its meaning. With a kind, prints its fields (JSON name, type, whether required, unit or allowed values) and an example that cicada apply accepts. The catalog is generated from the intent registry. See docs/manual/agents.md."},
	{name: "diagnostics", summary: "list the diagnostic codes with their meaning and fix", usage: "cicada diagnostics [--json] [code]", flags: "  --json      write the catalog as JSON (schema cicada.diagnostics.v1)\n  --help      show this help", notes: "Lists every diagnostic code with its severity and meaning. With a code (CICADA-PARAM or PARAM), prints where it is reported, what it means and how to fix it. docs/manual/diagnostics.md is generated from the same catalog."},
	{name: "fix", summary: "migrate legacy source spellings", usage: "cicada fix <score.cicada> [--all] [--check]", flags: "  --all       migrate every edition-1 score under the project folder together\n  --check     report required source or manifest changes without writing\n  --help      show this help", notes: "With --all, the score path is optional; run the command from the project folder."},
	{name: "check", summary: "validate one score or the current project", usage: "cicada check [--profile desktop|browser] [--max-resident-pcm <size>] [--json] [score.cicada]", flags: "  --profile <name>           limits to check against: desktop (default) or browser\n  --max-resident-pcm <size>  cap on decoded sampler-pack PCM, as for render, such as 2GiB (default 1GiB)\n  --json                     write the result as JSON (schema cicada.check.v1): each diagnostic with its position and any known fix\n  --cpuprofile <file>        write a Go CPU profile of the command to file\n  --help                     show this help", notes: "The browser profile also builds the images cicada wam2 exports and applies the portable limits: 16 tracks, 32 allocated voices, a 2 MiB image and no master processor. It lists the voices each track reserves and fails when a limit is exceeded. For a score with sampler packs, check reads each pack's manifest, loads the packs one at a time, and reports a pack that does not load at 44.1, 48 or 96 kHz, a note outside every zone of its pack, and packs whose declared size render would refuse. Pack paths resolve from the folder of cicada.mod, as audio assets do. CICADA_MAX_RESIDENT_PCM sets the cap when the option is omitted."},
	{name: "fmt", summary: "format a score or current project", usage: "cicada fmt [--check|-w] [score.cicada]", flags: "  --check     report formatting changes without writing\n  -w          write formatted source\n  --help      show this help"},
	{name: "play", summary: "play and watch a score", usage: "cicada play [score.cicada] [--audio tymbal|oto|null] [--max-resident-pcm <size>]", flags: "  --audio <name>             select tymbal, oto, or null\n  --max-resident-pcm <size>  cap on decoded sampler-pack PCM held in memory, such as 512MiB (default 1GiB)\n  --help                     show this help", notes: "CICADA_AUDIO selects the backend when --audio is omitted. Tymbal is the default on Windows and Linux; oto is the macOS default. Use --audio oto to select oto explicitly. Sampler packs stay in memory as decoded float32 PCM, and --max-resident-pcm caps their total; a score whose packs decode to more is refused, with the cap and the size measured in the message. A reloaded score reuses the packs already in memory; a reload that points a sampler at another pack holds both until the new score takes over at the next bar. CICADA_MAX_RESIDENT_PCM sets the cap when the option is omitted. Sizes are whole numbers with KiB, MiB, GiB, TiB, KB, MB, GB or TB, such as 512MiB."},
	{name: "studio", summary: "open the local workstation", usage: "cicada studio [score.cicada] [--listen 127.0.0.1:port] [--audio tymbal|oto|null] [--lsp-stdio] [--max-resident-pcm <size>]", flags: "  --listen <addr>            loopback address; default 127.0.0.1:0\n  --audio <name>             select tymbal, oto, or null\n  --lsp-stdio                serve LSP on stdin/stdout and Studio on stderr\n  --max-resident-pcm <size>  cap on decoded sampler-pack PCM held in memory, such as 2GiB (default 1GiB)\n  --help                     show this help", notes: "CICADA_AUDIO selects the backend when --audio is omitted. Tymbal is the default on Windows and Linux; oto is the macOS default. Use --audio oto to select oto explicitly. CICADA_MAX_RESIDENT_PCM sets the sampler-pack cap when --max-resident-pcm is omitted."},
	{name: "lsp", summary: "run the editor language server", usage: "cicada lsp", flags: "  (no command flags)"},
	{name: "fields", summary: "print the project field catalog as JSON", usage: "cicada fields", flags: "  (no command flags)"},
	{name: "params", summary: "print the parameter catalog as JSON", usage: "cicada params", flags: "  (no command flags)"},
	{name: "view", summary: "write a score or project view as HTML", usage: "cicada view <score.cicada|project.json> -o <view.html>", flags: "  -o <file>   required output HTML path\n  --help      show this help"},
	{name: "explain", summary: "explain a language item or parameter value", usage: "cicada explain <construct[.field]> [--json]\ncicada explain <score.cicada> <path> [@bar[.beat[.step]]]", flags: "  --json      print construct details as JSON (schema cicada.explain.v1)\n  --help      show this help", notes: "Score locations use bar numbers starting at 1."},
	{name: "gen", summary: "generate a deterministic phrase", usage: "cicada gen [flags]", flags: "  --seed <n>             64-bit generator seed (default 0)\n  --key <pitch-class>    tonic pitch class (default c)\n  --scale <name>         scale (default minor)\n  --root-octave <n>      root octave (default 2)\n  --steps <n>            steps per bar (default 16)\n  --density <n>          onset density (default 0.6)\n  --accent-density <n>   accent density (default 0.5)\n  --slide-density <n>    slide density (default 0.4)\n  --octave-jump <n>      octave-jump density (default 0.3)\n  --swing <percent>      swing percent (default 54)\n  --gate <percent>       gate percent (default 55)\n  --structure <name>     A, AABA, ABAB, ABAC, or AAAB (default aaba)\n  --voice <name>         voice of the bass track: a keyboard patch such as fm_bass, piano, or acid (default fm_bass)\n  --rest-downbeat        allow a downbeat rest\n  --trace                write the draw trace as JSON to stdout\n  -o <file>              write the score instead of stdout\n  --help                 show this help", notes: "The voice sets only the track line of the score; the bars and the --trace output are the same for every voice. Accents (^) and slides (~) are written for every voice but are audible mainly on acid, so --accent-density and --slide-density matter most there. The piano and the keyboard patches hold a slid note into the next one instead of gliding."},
	{name: "convert", summary: "convert source and semantic JSON", usage: "cicada convert <in.cicada|in.json> -o <out.json|out.cicada>", flags: "  -o <file>   required output path with matching extension\n  --help      show this help"},
	{name: "compare", summary: "compare two projects", usage: "cicada compare --semantic <a> <b>", flags: "  --semantic  compare compiled project meaning\n  --help      show this help"},
	{name: "highlight", summary: "print source with syntax highlighting", usage: "cicada highlight [--html|--spans] <score.cicada>", flags: "  --html    write HTML highlighting\n  --spans   write JSON highlighting spans\n  --help    show this help"},
	{name: "song", summary: "read a score or list the project's scores", usage: "cicada song [--json] [--include source,symbols,structure] [score.cicada]", flags: "  --json             write the result as JSON (schema cicada.song.v1)\n  --include <parts>  what to give of the score: source (default), symbols, structure\n  --help             show this help", notes: "Without a score, lists every score of the project with its revision. With one, gives its revision (the form cicada apply checks) and its source text, the names it defines, or its compiled meaning as semantic JSON."},
	{name: "symbols", summary: "list source definitions and references", usage: "cicada symbols [--refs] [--json] <score.cicada>", flags: "  --refs   include references as well as definitions\n  --json   write the symbol list as JSON\n  --help   show this help"},
	{name: "render", summary: "render a score to WAV", usage: "cicada render <score.cicada> -o <out.wav> [flags] [--json]", flags: "  -o <file>                    required output WAV path\n  --rate <Hz>                  sample rate (default 48000)\n  --bits <16|24|32>            WAV bit depth (default 24)\n  --from <bar>                 one-based start bar (default 1)\n  --bars <n>                   bars to render; 0 means through song end\n  --tail <duration>            render tail, 0s to 60s (default 3s)\n  --dither=<bool>              deterministic TPDF dither for integer PCM (default true)\n  --normalize=<bool>           peak normalize to -1 dBFS (default false)\n  --block <frames>             offline block size (default 4096)\n  --loudness <LUFS>            target integrated loudness\n  --true-peak-max <dBTP>       maximum true peak for loudness rendering (default -1)\n  --loudness-tolerance <LU>    target tolerance (default 0.5)\n  --sampler-baseline           compare one recorded layer/take and 2 ms release\n  --max-resident-pcm <size>    cap on decoded sampler-pack PCM held in memory, such as 512MiB (default 1GiB)\n  --export <name>              use a named export profile\n  --jobs auto|<n>              render lanes: tracks synthesized at once; absent or 1 renders serially, auto picks the smaller of 4 and half the CPUs, <n> is honoured up to the number of CPUs\n  --json                       write what was rendered as JSON (schema cicada.render.v1); on failure, an error with a reason code\n  --cpuprofile <file>          write a Go CPU profile of the command to file\n  --help                       show this help", notes: "Use --from 1 for the first bar. The legacy value --from 0 is accepted with a warning and means bar 1. Loudness targets (--loudness, or an export block with loudness) add a static gain. Without a master limiter the direct renderer applies it after the master chain, which keeps crest factor and can miss an ambitious target; the scheduled renderer (arrangements, audio clips, serial track effects, sample packs) applies it before the chain, as it always has. If the score's master chain contains a limiter, the gain goes in before the chain so that limiter holds the true-peak ceiling, capped at +12 dB of input gain and 6 dB of limiter gain reduction; a target that needs more is reported as a shortfall. Sampler packs stay in memory as decoded float32 PCM, and --max-resident-pcm caps their total; a project whose packs decode to more is refused before it renders, with the cap and the size measured in the message. CICADA_MAX_RESIDENT_PCM sets the cap when the option is omitted. Sizes are whole numbers with KiB, MiB, GiB, TiB, KB, MB, GB or TB, such as 512MiB. Every --jobs value writes the same bytes; the summary line and --json report the lanes the render set up and ran, and why it stayed serial if it did."},
	{name: "stems", summary: "render separate float32 WAV stems", usage: "cicada stems <score.cicada> -o <directory> [flags]", flags: "  -o <directory>             required output directory\n  --rate <Hz>                sample rate (default 48000)\n  --bits 32                  float32 output only\n  --from <bar>               one-based start bar (default 1)\n  --bars <n>                 bars to render; 0 means through song end\n  --tail <duration>          render tail, 0s to 60s (default 3s)\n  --dither=<bool>            deterministic TPDF dither option\n  --normalize=<bool>         peak normalization option\n  --block <frames>           offline block size (default 4096)\n  --max-resident-pcm <size>  cap on decoded sampler-pack PCM held in memory, such as 512MiB (default 1GiB)\n  --jobs auto|<n>            render lanes; absent or 1 renders serially, auto picks the smaller of 4 and half the CPUs, <n> is honoured up to the number of CPUs\n  --json                     list the stem files as JSON (schema cicada.render.v1)\n  --cpuprofile <file>        write a Go CPU profile of the command to file\n  --help                     show this help", notes: "Use --from 1 for the first bar. The legacy value --from 0 is accepted with a warning and means bar 1."},
	{name: "audition", summary: "report what a score sounds like", usage: "cicada audition <score.cicada> [flags]", flags: "  -o <directory>             write spectrogram.png, chroma.png and rhythm.png\n  --from <bar>               one-based start bar (default 1)\n  --bars <n>                 bars to analyze; 0 means through song end\n  --json                     write the report as JSON (format cicada.audition/1)\n  --max-resident-pcm <size>  cap on decoded sampler-pack PCM held in memory, such as 2GiB (default 1GiB)\n  --jobs auto|<n>            render lanes; absent or 1 renders serially, auto picks the smaller of 4 and half the CPUs, <n> is honoured up to the number of CPUs\n  --cpuprofile <file>        write a Go CPU profile of the command to file\n  --help                     show this help", notes: "Renders the mix and stems, then reports heard key, written and heard chords per bar, and per-track level, brightness, attack, decay and rhythm. See docs/manual/audition.md. CICADA_MAX_RESIDENT_PCM sets the sampler-pack cap when the option is omitted."},
	{name: "verify-wav", summary: "verify WAV samples and timing", usage: "cicada verify-wav <file.wav> [flags]", flags: "  --rate <Hz>                 expected sample rate (default 48000)\n  --bits <16|24|32>           expected bit depth (default 24)\n  --from <bar>                one-based start bar (default 1)\n  --bars <n>                  expected bars (required)\n  --tail <duration>           expected tail, 0s to 60s (default 3s)\n  --peak-max-db <dBFS>        peak ceiling (default -0.3)\n  --dc-max-db <dBFS>          DC ceiling (default -60)\n  --lufs <LUFS>               integrated-loudness target\n  --lufs-tolerance <LU>       loudness tolerance (default 0.5)\n  --true-peak-max <dBTP>      true-peak ceiling\n  --report                    write JSON report to stderr\n  --help                      show this help", notes: "Use --from 1 for the first bar. The legacy value --from 0 is accepted with a warning and means bar 1."},
	{name: "verify-stems", summary: "verify a stem directory", usage: "cicada verify-stems <directory> [flags]", flags: "  --tap <name>             verification tap (default pre-comp)\n  --residual-max-db <dBFS> maximum bus-sum residual (default -80)\n  --help                   show this help"},
	{name: "wam2", summary: "export a WAM2 instrument", usage: "cicada wam2 <score.cicada> -o <directory> [flags]", flags: "  -o <directory>  required output plugin directory\n  --kernel <file>  kernel WASM path (default build/cicada-kernel.wasm)\n  --sdk <directory> WAM SDK directory (default build/wam2-sdk/node_modules/@webaudiomodules/sdk)\n  --midi-track <id> MIDI instrument track (default first supported track)\n  --help          show this help"},
	{name: "director", summary: "write the Game Director manifest for browser hosts", usage: "cicada director <score.cicada> -o <director.json> [--rate <Hz>]", flags: "  -o <file>    required output JSON path\n  --rate <Hz>  manifest sample rate: 44100, 48000 or 96000 (default 48000)\n  --help       show this help", notes: "Resolves the score's live block (states, macros, stingers and transitions) to the kernel IDs and frame counts that @cicada/game-director and the Go sdk/director package read. The audio context must run at the manifest's rate. A score without a live block, or with more than 16 tracks, is refused. An existing output file is replaced."},
	{name: "game-score", summary: "compile a score into a score pack for a GoSX game", usage: "cicada game-score <score.cicada> -o <score.cscore> [--rate <Hz>]", flags: "  -o <file>    required output path, ending in .cscore\n  --rate <Hz>  sample rate: 44100, 48000 or 96000 (default 48000)\n  --help       show this help", notes: "Writes one file with the compiled kernel image and the Game Director manifest (states, macros, stingers and transitions) at one sample rate. A GoSX game loads it with sdk/gosxhost and plays it in an AudioWorklet; see docs/manual/gosx-game.md. The score needs a live block. The output names the kernel module the score needs. An existing output file is replaced."},
	{name: "midi", summary: "export a Standard MIDI File", usage: "cicada midi <score.cicada> [flags]", flags: "  -o <file>       required output MIDI path\n  --bars <n>      song bars; 0 means all (default 0)\n  --pattern <id>  export only one pattern\n  --report        write JSON report to stderr\n  --help          show this help", notes: "The file holds the notes and velocities the renderer plays. Chance steps (?N) and humanize draw from the pattern seed, track, slot, pass and step, as playback does, so each pass of a pattern keeps its own chance decisions and velocity offsets. Both renderers make the same draws."},
	{name: "verify-midi", summary: "verify a Standard MIDI File", usage: "cicada verify-midi <file.mid> [flags]", flags: "  --ppq <n>     expected pulses per quarter note (default 960)\n  --type <n>    expected SMF type (default 1)\n  --report      write JSON report to stderr\n  --help        show this help"},
	{name: "compare-midi", summary: "compare MIDI event streams", usage: "cicada compare-midi <a.mid> <b.mid>", flags: "  (no command flags)"},
	{name: "import-midi", summary: "import MIDI notes as a score", usage: "cicada import-midi <file.mid> -o <score.cicada> [flags]", flags: "  -o <file>           required new score path\n  --quantize <step>    explicitly quantize onsets; default infers an exact grid\n  --report            write JSON import report to stderr\n  --help              show this help", notes: "Imports fixed-tempo 4/4 notes and velocity. Each MIDI track keeps its own named tracks; notes that start together with one length become a chord, and drums share one drums track. Timing edits, cross-bar retriggers, and velocity edits are counted. Controller, program and expression events are not imported."},
	{name: "golden", summary: "check the reference render fingerprint", usage: "cicada golden [flags]", flags: "  --update       write the golden fingerprint\n  --score <file> score to render (default examples/first-acid.cicada)\n  --out <file>   fingerprint path (default testdata/golden/first-acid.fp)\n  --rate <Hz>    sample rate (default 48000)\n  --bars <n>     bars to render (default 8)\n  --help         show this help"},
	{name: "validate", summary: "validate one score", usage: "cicada validate <score.cicada>", flags: "  (no command flags)"},
	{name: "ast", summary: "print the typed syntax tree as JSON", usage: "cicada ast <score.cicada>", flags: "  (no command flags)"},
	{name: "events", summary: "print events for one pattern", usage: "cicada events <score.cicada> <track> <pattern>", flags: "  (no command flags)"},
	{name: "graph", summary: "print one compiled instrument graph", usage: "cicada graph <score.cicada> <instrument>", flags: "  (no command flags)"},
}

// overviewGroups are the lines of the command overview. Every command main
// dispatches is in exactly one; TestOverviewListsEveryCommand checks it.
var overviewGroups = [][]string{
	{"agents", "mcp", "new", "bundle", "save-as", "lib", "apply", "intents", "fix", "check", "diagnostics", "fmt", "play", "studio", "lsp"},
	{"render", "stems", "audition", "verify-wav", "verify-stems", "midi", "verify-midi", "compare-midi", "import-midi"},
	{"gen", "explain", "view", "highlight", "song", "symbols", "convert", "compare", "validate", "ast"},
	{"events", "graph", "fields", "params", "golden", "record-pack", "record-kit", "fit-model", "transcribe", "wam2", "director", "game-score"},
}

var shortHelpText = func() string {
	var text strings.Builder
	text.WriteString("Usage: cicada <command> [arguments]\n\n")
	text.WriteString("Create a project, edit and validate scores, play them in Studio, or render files.\n\n")
	text.WriteString("Commands:\n")
	for _, group := range overviewGroups {
		text.WriteString("  " + strings.Join(group, ", ") + "\n")
	}
	text.WriteString("\nRun \"cicada help <command>\" for usage and flags, or \"cicada help --json\" for every command as JSON.")
	text.WriteString("\nAn AI agent starts with \"cicada agents\": the quick start for driving Cicada from a program.")
	return text.String()
}()

func handleCLIHelp(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) > 0 && args[0] != "help" && hasJSONFlag(args) {
		if handled, code := handleJSONHelp(args, stdout, stderr); handled {
			return true, code
		}
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, shortHelpText)
		return true, 0
	}
	if args[0] == "help" {
		if json, name := helpJSONArgs(args[1:]); json {
			if !writeHelpJSON(stdout, name) {
				message := fmt.Sprintf("cicada: unknown command %q", name)
				writeAgentJSON(stdout, jsonErrorDocument{Schema: helpSchema, Error: jsonError{Code: "unknown_command", Message: message}})
				fmt.Fprintln(stderr, message)
				fmt.Fprintln(stderr, "run cicada help")
				return true, 2
			}
			return true, 0
		}
		if len(args) == 1 {
			fmt.Fprintln(stdout, shortHelpText)
			return true, 0
		}
		if len(args) != 2 && !(len(args) == 3 && args[1] == "lib") {
			fmt.Fprintln(stderr, "usage: cicada help <command>")
			fmt.Fprintln(stderr, "run cicada help")
			return true, 2
		}
		if entry, ok := findCommandHelp(strings.Join(args[1:], " ")); ok {
			fmt.Fprintln(stdout, renderCommandHelp(entry))
			return true, 0
		}
		fmt.Fprintf(stderr, "cicada: unknown command %q\n", args[1])
		fmt.Fprintln(stderr, "run cicada help")
		return true, 2
	}
	helpName := args[0]
	if helpName == "lib" && len(args) > 1 {
		if _, ok := findCommandHelp("lib " + args[1]); ok {
			helpName += " " + args[1]
		}
	}
	if entry, ok := findCommandHelp(helpName); ok {
		for _, arg := range args[1:] {
			if arg == "--help" || arg == "-h" {
				fmt.Fprintln(stdout, renderCommandHelp(entry))
				return true, 0
			}
		}
		return false, 0
	}
	if !strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "cicada: unknown command %q\n", args[0])
		fmt.Fprintln(stderr, "run cicada help")
		return true, 2
	}
	return false, 0
}

// handleJSONHelp answers the help requests of a run with --json: --help with
// no command prints the help document, <command> --help prints that
// command's entry, and an unknown command prints an error document.
func handleJSONHelp(args []string, stdout, stderr io.Writer) (bool, int) {
	onlyFlags := true
	for _, arg := range args {
		onlyFlags = onlyFlags && (arg == "--json" || arg == "--help" || arg == "-h")
	}
	if onlyFlags {
		writeHelpJSON(stdout, "")
		return true, 0
	}
	name := args[0]
	if name == "lib" && len(args) > 1 {
		if _, ok := findCommandHelp("lib " + args[1]); ok {
			name += " " + args[1]
		}
	}
	if _, ok := findCommandHelp(name); ok {
		for _, arg := range args[1:] {
			if arg == "--help" || arg == "-h" {
				writeHelpJSON(stdout, name)
				return true, 0
			}
		}
		return false, 0
	}
	if strings.HasPrefix(name, "-") {
		return false, 0
	}
	message := fmt.Sprintf("cicada: unknown command %q", name)
	writeAgentJSON(stdout, jsonErrorDocument{Schema: helpSchema, Error: jsonError{Code: "unknown_command", Message: message}})
	fmt.Fprintln(stderr, message)
	fmt.Fprintln(stderr, "run cicada help")
	return true, 2
}

// helpJSONArgs reports whether the arguments after help ask for JSON, and the
// command they name, if any: help --json [command] or help <command> --json.
func helpJSONArgs(args []string) (bool, string) {
	asJSON := false
	var names []string
	for _, arg := range args {
		if arg == "--json" {
			asJSON = true
		} else {
			names = append(names, arg)
		}
	}
	return asJSON, strings.Join(names, " ")
}

// extraCommandHelpEntries are commands whose help stays out of
// all-commands.golden: lib and its subcommands, save-as and the recording
// tools.
var extraCommandHelpEntries = []commandHelpEntry{
	{name: "transcribe", summary: "transcribe a sung, hummed or solo-instrument WAV into score rows", usage: "cicada transcribe <recording.wav> [flags]", flags: "  --tempo <BPM>  20–300 BPM; 0 infers tempo (default 0)\n  --key <key>    auto or tonic major/minor (default auto)\n  --grid <grid>  1/4, 1/8 or 1/16 (default 1/16)\n  -o <file>      write a new score file instead of stdout\n  --report       write measured notes and confidence JSON to stderr\n  --help         show this help", notes: "Analysis stays local and runs outside playback. Choose one monophonic PCM WAV. Score rows snap timing to the selected grid; sparse tempo, meter and key estimates require review. Existing output files are preserved."},
	{name: "fit-model", summary: "fit a recorded hit to a playable modal instrument", usage: "cicada fit-model -o <pack-directory> [flags] <recording.wav>", flags: "  -o <directory>  required new pack directory\n  --name <id>     instrument name (default recorded_model)\n  --hit <n>       detected hit, one-based (default 1)\n  --help          show this help", notes: "Put flags before the input. Writes measured model.json, checksummed modal renders and a playable pinned score; user recording provenance is retained."},
	{name: "record-kit", summary: "build one drum-kit sampler pack from your own recorded takes", usage: "cicada record-kit -o <pack-directory> [flags] <recipe.json>", flags: "  -o <directory>  required new pack directory\n  --name <id>     lowercase kit name (default kit)\n  --help          show this help", notes: "Put flags before the recipe. The recipe names each piece's MIDI note and its take WAV files, which are found next to the recipe; see docs/manual/recording-instruments.md. Prints the hits and layers found for each piece and a checksummed sampler declaration. Warns about takes to record again: tails cut off, hits that merged or clipped, and takes with no room tone at the start. Audio carries the owner recording label, which records provenance and grants no redistribution rights."},
	{name: "record-pack", summary: "slice user WAV recordings into a pinned sampler pack", usage: "cicada record-pack -o <pack-directory> [flags] <recording.wav>...", flags: "  -o <directory>       required new pack directory\n  --name <id>          lowercase instrument name (default recorded)\n  --root <MIDI>        fallback root, 12–95 (default 60)\n  --layers <n>         velocity layers, 1–8 (default 3)\n  --auto-pitch=<bool>  map detected roots (default false)\n  --help               show this help", notes: "Put flags before input files. Prints a checksummed sampler declaration; audio retains the user recording licence."},
	{name: "lib", summary: "manage pinned source and audio libraries", usage: "cicada lib list\ncicada lib show PATH\ncicada lib new PATH [--dir DIR]\ncicada lib update [PATH]\ncicada lib vendor", notes: "Use cicada help lib <command> for details. CICADA_LIBRARY overrides the user config directory's cicada/lib folder."},
	{name: "lib list", summary: "list available std, project, and user libraries", usage: "cicada lib list [--json]", flags: "  --json      write the list as JSON (schema cicada.library.v1)\n  --help      show this help", notes: "Prints each path and resolution kind, including duplicates. Does not change pins."},
	{name: "lib show", summary: "inspect a resolved library", usage: "cicada lib show PATH [--json]", flags: "  --json      write the library as JSON (schema cicada.library.v1)\n  --help      show this help", notes: "Prints the manifest, declaration names, audio paths, SHA-256 hash, and resolution directory. Does not change pins."},
	{name: "lib new", summary: "scaffold an edition-2 library", usage: "cicada lib new PATH [--dir DIR]", flags: "  --dir DIR   create PATH in an explicit directory\n  --help      show this help", notes: "A library path such as demo/tone is created in the user library. An absolute or dot-prefixed directory uses its final directory name as the library path. --dir preserves the full library path. Existing destinations are refused; std is reserved."},
	{name: "lib update", summary: "pin imported library content", usage: "cicada lib update [PATH]", notes: "Update one imported library, or all imports when PATH is omitted. Prints old and new resolution kinds and SHA-256 hashes. Use only after an intended change."},
	{name: "lib vendor", summary: "copy imported user libraries into project lib/", usage: "cicada lib vendor", notes: "Verifies current pins, stages and verifies all copies, then publishes libraries and updated pins. Includes transitive imports. Refuses different existing libraries and rolls back new copies on failure. Standard libraries stay embedded."},
	{name: "save-as", summary: "copy a score, audio, and pinned libraries", usage: "cicada save-as <score.cicada> <target.cicada>", flags: "  (no command flags)", notes: "Imported user and project libraries are automatically vendored and re-pinned before the target score is published."},
}

func findCommandHelp(name string) (commandHelpEntry, bool) {
	for _, entries := range [][]commandHelpEntry{commandHelpEntries, extraCommandHelpEntries} {
		for _, entry := range entries {
			if entry.name == name {
				return entry, true
			}
		}
	}
	return commandHelpEntry{}, false
}

func renderCommandHelp(entry commandHelpEntry) string {
	var output strings.Builder
	flags := entry.flags
	if !strings.Contains(flags, "--help") {
		flags += "\n  --help      show this help"
	}
	fmt.Fprintf(&output, "%s\n\n%s\n\nFlags:\n%s\n", entry.usage, entry.summary, flags)
	if entry.notes != "" {
		fmt.Fprintf(&output, "\n%s\n", entry.notes)
	}
	return strings.TrimRight(output.String(), "\n")
}
