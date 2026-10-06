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
	{name: "new", summary: "create an edition-2 project", usage: "cicada new <name>", flags: "  (no command flags)"},
	{name: "fix", summary: "migrate legacy source spellings", usage: "cicada fix <score.cicada> [--all] [--check]", flags: "  --all       migrate every edition-1 score under the project folder together\n  --check     report required source or manifest changes without writing\n  --help      show this help", notes: "With --all, the score path is optional; run the command from the project folder."},
	{name: "check", summary: "validate one score or the current project", usage: "cicada check [score.cicada]", flags: "  (no command flags)"},
	{name: "fmt", summary: "format a score or current project", usage: "cicada fmt [--check|-w] [score.cicada]", flags: "  --check     report formatting changes without writing\n  -w          write formatted source\n  --help      show this help"},
	{name: "play", summary: "play and watch a score", usage: "cicada play [score.cicada] [--audio tymbal|oto|null]", flags: "  --audio <name>  select tymbal, oto, or null\n  --help          show this help", notes: "CICADA_AUDIO selects the backend when --audio is omitted. Tymbal is the default on Windows and Linux; oto is the macOS default. Use --audio oto to select oto explicitly."},
	{name: "studio", summary: "open the local workstation", usage: "cicada studio [score.cicada] [--listen 127.0.0.1:port] [--audio tymbal|oto|null] [--lsp-stdio]", flags: "  --listen <addr>  loopback address; default 127.0.0.1:0\n  --audio <name>   select tymbal, oto, or null\n  --lsp-stdio      serve LSP on stdin/stdout and Studio on stderr\n  --help           show this help", notes: "CICADA_AUDIO selects the backend when --audio is omitted. Tymbal is the default on Windows and Linux; oto is the macOS default. Use --audio oto to select oto explicitly."},
	{name: "lsp", summary: "run the editor language server", usage: "cicada lsp", flags: "  (no command flags)"},
	{name: "fields", summary: "print the project field catalog as JSON", usage: "cicada fields", flags: "  (no command flags)"},
	{name: "params", summary: "print the parameter catalog as JSON", usage: "cicada params", flags: "  (no command flags)"},
	{name: "view", summary: "write a score or project view as HTML", usage: "cicada view <score.cicada|project.json> -o <view.html>", flags: "  -o <file>   required output HTML path\n  --help      show this help"},
	{name: "explain", summary: "explain a language item or parameter value", usage: "cicada explain <construct[.field]> [--json]\ncicada explain <score.cicada> <path> [@bar[.beat[.step]]]", flags: "  --json      print construct details as JSON\n  --help      show this help", notes: "Score locations use bar numbers starting at 1."},
	{name: "gen", summary: "generate a deterministic phrase", usage: "cicada gen [flags]", flags: "  --seed <n>             64-bit generator seed (default 0)\n  --key <pitch-class>    tonic pitch class (default c)\n  --scale <name>         scale (default minor)\n  --root-octave <n>      root octave (default 2)\n  --steps <n>            steps per bar (default 16)\n  --density <n>          onset density (default 0.6)\n  --accent-density <n>   accent density (default 0.5)\n  --slide-density <n>    slide density (default 0.4)\n  --octave-jump <n>      octave-jump density (default 0.3)\n  --swing <percent>      swing percent (default 54)\n  --gate <percent>       gate percent (default 55)\n  --structure <name>     A, AABA, ABAB, ABAC, or AAAB (default aaba)\n  --rest-downbeat        allow a downbeat rest\n  --trace                write the draw trace as JSON to stdout\n  -o <file>              write the score instead of stdout\n  --help                 show this help"},
	{name: "convert", summary: "convert source and semantic JSON", usage: "cicada convert <in.cicada|in.json> -o <out.json|out.cicada>", flags: "  -o <file>   required output path with matching extension\n  --help      show this help"},
	{name: "compare", summary: "compare two projects", usage: "cicada compare --semantic <a> <b>", flags: "  --semantic  compare compiled project meaning\n  --help      show this help"},
	{name: "highlight", summary: "print source with syntax highlighting", usage: "cicada highlight [--html|--spans] <score.cicada>", flags: "  --html    write HTML highlighting\n  --spans   write JSON highlighting spans\n  --help    show this help"},
	{name: "symbols", summary: "list source definitions and references", usage: "cicada symbols [--refs] [--json] <score.cicada>", flags: "  --refs   include references as well as definitions\n  --json   write the symbol list as JSON\n  --help   show this help"},
	{name: "render", summary: "render a score to WAV", usage: "cicada render <score.cicada> -o <out.wav> [flags]", flags: "  -o <file>                    required output WAV path\n  --rate <Hz>                  sample rate (default 48000)\n  --bits <16|24|32>            WAV bit depth (default 24)\n  --from <bar>                 one-based start bar (default 1)\n  --bars <n>                   bars to render; 0 means through song end\n  --tail <duration>            render tail (default 3s)\n  --dither=<bool>              deterministic TPDF dither for integer PCM (default true)\n  --normalize=<bool>           peak normalize to -1 dBFS (default false)\n  --block <frames>             offline block size (default 4096)\n  --loudness <LUFS>            target integrated loudness\n  --true-peak-max <dBTP>       maximum true peak for loudness rendering (default -1)\n  --loudness-tolerance <LU>    target tolerance (default 0.5)\n  --sampler-baseline           compare one recorded layer/take and 2 ms release\n  --export <name>              use a named export profile\n  --help                       show this help", notes: "Use --from 1 for the first bar. The legacy value --from 0 is accepted with a warning and means bar 1."},
	{name: "stems", summary: "render separate float32 WAV stems", usage: "cicada stems <score.cicada> -o <directory> [flags]", flags: "  -o <directory>           required output directory\n  --rate <Hz>              sample rate (default 48000)\n  --bits 32                float32 output only\n  --from <bar>             one-based start bar (default 1)\n  --bars <n>               bars to render; 0 means through song end\n  --tail <duration>        render tail (default 3s)\n  --dither=<bool>          deterministic TPDF dither option\n  --normalize=<bool>       peak normalization option\n  --block <frames>         offline block size (default 4096)\n  --help                   show this help", notes: "Use --from 1 for the first bar. The legacy value --from 0 is accepted with a warning and means bar 1."},
	{name: "verify-wav", summary: "verify WAV samples and timing", usage: "cicada verify-wav <file.wav> [flags]", flags: "  --rate <Hz>                 expected sample rate (default 48000)\n  --bits <16|24|32>           expected bit depth (default 24)\n  --from <bar>                one-based start bar (default 1)\n  --bars <n>                  expected bars (required)\n  --tail <duration>           expected tail (default 3s)\n  --peak-max-db <dBFS>        peak ceiling (default -0.3)\n  --dc-max-db <dBFS>          DC ceiling (default -60)\n  --lufs <LUFS>               integrated-loudness target\n  --lufs-tolerance <LU>       loudness tolerance (default 0.5)\n  --true-peak-max <dBTP>      true-peak ceiling\n  --report                    write JSON report to stderr\n  --help                      show this help", notes: "Use --from 1 for the first bar. The legacy value --from 0 is accepted with a warning and means bar 1."},
	{name: "verify-stems", summary: "verify a stem directory", usage: "cicada verify-stems <directory> [flags]", flags: "  --tap <name>             verification tap (default pre-comp)\n  --residual-max-db <dBFS> maximum bus-sum residual (default -80)\n  --help                   show this help"},
	{name: "midi", summary: "export a Standard MIDI File", usage: "cicada midi <score.cicada> [flags]", flags: "  -o <file>       required output MIDI path\n  --bars <n>      song bars; 0 means all (default 0)\n  --pattern <id>  export only one pattern\n  --report        write JSON report to stderr\n  --help          show this help"},
	{name: "verify-midi", summary: "verify a Standard MIDI File", usage: "cicada verify-midi <file.mid> [flags]", flags: "  --ppq <n>     expected pulses per quarter note (default 960)\n  --type <n>    expected SMF type (default 1)\n  --report      write JSON report to stderr\n  --help        show this help"},
	{name: "compare-midi", summary: "compare MIDI event streams", usage: "cicada compare-midi <a.mid> <b.mid>", flags: "  (no command flags)"},
	{name: "import-midi", summary: "import MIDI notes as a score", usage: "cicada import-midi <file.mid> -o <score.cicada> [flags]", flags: "  -o <file>           required new score path\n  --quantize <step>    explicitly quantize onsets; default infers an exact grid\n  --report            write JSON import report to stderr\n  --help              show this help", notes: "Imports fixed-tempo 4/4 notes and velocity. Timing edits, cross-bar retriggers, and drum velocity quantization are counted. Controller, program and expression events are not imported."},
	{name: "golden", summary: "check the reference render fingerprint", usage: "cicada golden [flags]", flags: "  --update       write the golden fingerprint\n  --score <file> score to render (default examples/first-acid.cicada)\n  --out <file>   fingerprint path (default testdata/golden/first-acid.fp)\n  --rate <Hz>    sample rate (default 48000)\n  --bars <n>     bars to render (default 8)\n  --help         show this help"},
	{name: "validate", summary: "validate one score", usage: "cicada validate <score.cicada>", flags: "  (no command flags)"},
	{name: "ast", summary: "print the typed syntax tree as JSON", usage: "cicada ast <score.cicada>", flags: "  (no command flags)"},
	{name: "events", summary: "print events for one pattern", usage: "cicada events <score.cicada> <track> <pattern>", flags: "  (no command flags)"},
	{name: "graph", summary: "print one compiled instrument graph", usage: "cicada graph <score.cicada> <instrument>", flags: "  (no command flags)"},
}

var shortHelpText = "Usage: cicada <command> [arguments]\n\n" +
	"Create a project, edit and validate scores, play them in Studio, or render files.\n\n" +
	"Commands:\n" +
	"  new, fix, check, fmt, play, studio, lsp\n" +
	"  render, stems, verify-wav, verify-stems, midi, verify-midi, compare-midi, import-midi\n" +
	"  gen, explain, view, highlight, symbols, convert, compare, validate, ast\n" +
	"  events, graph, fields, params, golden, record-pack, fit-model\n\n" +
	"Run \"cicada help <command>\" for usage and flags."

func handleCLIHelp(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, shortHelpText)
		return true, 0
	}
	if args[0] == "help" {
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

func findCommandHelp(name string) (commandHelpEntry, bool) {
	if name == "fit-model" {
		return commandHelpEntry{name: "fit-model", summary: "fit a recorded hit to a playable modal instrument", usage: "cicada fit-model -o <pack-directory> [flags] <recording.wav>", flags: "  -o <directory>  required new pack directory\n  --name <id>     instrument name (default recorded_model)\n  --hit <n>       detected hit, one-based (default 1)\n  --help          show this help", notes: "Put flags before the input. Writes measured model.json, checksummed modal renders and a playable pinned score; user recording provenance is retained."}, true
	}
	if name == "record-pack" {
		return commandHelpEntry{name: "record-pack", summary: "slice user WAV recordings into a pinned sampler pack", usage: "cicada record-pack -o <pack-directory> [flags] <recording.wav>...", flags: "  -o <directory>       required new pack directory\n  --name <id>          lowercase instrument name (default recorded)\n  --root <MIDI>        fallback root, 12–95 (default 60)\n  --layers <n>         velocity layers, 1–8 (default 3)\n  --auto-pitch=<bool>  map detected roots (default false)\n  --help               show this help", notes: "Put flags before input files. Prints a checksummed sampler declaration; audio retains the user recording licence."}, true
	}
	libEntries := []commandHelpEntry{
		{name: "lib", summary: "manage pinned source and audio libraries", usage: "cicada lib list\ncicada lib show PATH\ncicada lib new PATH [--dir DIR]\ncicada lib update [PATH]\ncicada lib vendor", notes: "Use cicada help lib <command> for details. CICADA_LIBRARY overrides the user config directory's cicada/lib folder."},
		{name: "lib list", summary: "list available std, project, and user libraries", usage: "cicada lib list", notes: "Prints each path and resolution kind, including duplicates. Does not change pins."},
		{name: "lib show", summary: "inspect a resolved library", usage: "cicada lib show PATH", notes: "Prints the manifest, declaration names, audio paths, SHA-256 hash, and resolution directory. Does not change pins."},
		{name: "lib new", summary: "scaffold an edition-2 library", usage: "cicada lib new PATH [--dir DIR]", flags: "  --dir DIR   create PATH in an explicit directory\n  --help      show this help", notes: "A library path such as demo/tone is created in the user library. An absolute or dot-prefixed directory uses its final directory name as the library path. --dir preserves the full library path. Existing destinations are refused; std is reserved."},
		{name: "lib update", summary: "pin imported library content", usage: "cicada lib update [PATH]", notes: "Update one imported library, or all imports when PATH is omitted. Prints old and new resolution kinds and SHA-256 hashes. Use only after an intended change."},
		{name: "lib vendor", summary: "copy imported user libraries into project lib/", usage: "cicada lib vendor", notes: "Verifies current pins, stages and verifies all copies, then publishes libraries and updated pins. Includes transitive imports. Refuses different existing libraries and rolls back new copies on failure. Standard libraries stay embedded."},
	}
	for _, entry := range libEntries {
		if entry.name == name {
			return entry, true
		}
	}
	if name == "save-as" {
		return commandHelpEntry{name: "save-as", summary: "copy a score, audio, and pinned libraries", usage: "cicada save-as <score.cicada> <target.cicada>", flags: "  (no command flags)", notes: "Imported user and project libraries are automatically vendored and re-pinned before the target score is published."}, true
	}
	for _, entry := range commandHelpEntries {
		if entry.name == name {
			return entry, true
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
