// Package diagnostics is the catalog of the diagnostic codes (CICADA-*) that
// cicada reports: each code's severities, where it is reported, what it
// means and how to fix it. cicada diagnostics prints it and
// docs/manual/diagnostics.md is generated from it. Its tests fail when the
// source emits a code the catalog lacks, or the catalog keeps a code nothing
// emits.
package diagnostics

import "sort"

// Code describes one diagnostic code.
//
// Severities are "error", "warning" or both. Scopes say where the code is
// reported:
//   - score: validating a score, so cicada check (and check --json), validate,
//     render and the language server report it with a source position;
//   - command: the error of a command such as lib, bundle, convert, director
//     or render's export settings, as text;
//   - studio: responses of Studio, the browser host or the public demo;
//   - edit: refusals of edit intents (cicada apply and Studio).
type Code struct {
	Code       string   `json:"code"`
	Severities []string `json:"severities"`
	Scopes     []string `json:"scopes"`
	Meaning    string   `json:"meaning"`
	Fix        string   `json:"fix"`
	Reasons    []Reason `json:"reasons,omitempty"`
}

// Reason is one value of the reason field that a code carries as data.
type Reason struct {
	Reason  string `json:"reason"`
	Meaning string `json:"meaning"`
}

var (
	errorOnly   = []string{"error"}
	warningOnly = []string{"warning"}
	both        = []string{"error", "warning"}
	score       = []string{"score"}
)

// codes is the catalog, in alphabetical order.
var codes = []Code{
	{"CICADA-ARRANGEMENT", errorOnly, score, "an arrange block is inconsistent: it is used together with a song block, it has no placements, a placement's pattern does not suit its track, or a step pattern placement starts between steps", "use either song or arrange, give arrange at least one placement, place patterns on tracks of their kind, and start step patterns on a step (check --json offers a moveclip fix for that one)", nil},
	{"CICADA-ASSET-FORMAT", errorOnly, score, "an audio asset's declared format, frames, rate or channels are invalid or differ from the WAV file", "declare format = wav and the file's real frames, rate (8000 to 384000 Hz) and channels (1 or 2); the message gives the expected and actual values", nil},
	{"CICADA-ASSET-HASH", errorOnly, score, "an audio asset's sha256 is malformed or does not match the file's bytes", "write the file's SHA-256 as 64 lowercase hex digits in quotes, or restore the file the score pinned", nil},
	{"CICADA-ASSET-MISSING", errorOnly, score, "an audio asset's file, or the project or library folder it lives in, cannot be read", "put the file at the declared path, relative to the folder of cicada.mod (or of the score when there is none)", nil},
	{"CICADA-ASSET-PATH", errorOnly, score, "an audio asset's path leaves the project folder, or a library asset is not under audio/", "use a project-relative path without .. that stays inside the project; library assets live under audio/", nil},
	{"CICADA-AUDIO", errorOnly, []string{"studio"}, "browser audio is not ready for the request: transport stopped, worklet send failed, or a reset is pending", "start browser audio and the transport, or wait for the fresh worklet kernel image after Stop, then retry", nil},
	{"CICADA-CAPABILITY", errorOnly, []string{"studio"}, "the public demo session does not offer this command", "use the command in a local cicada studio session", nil},
	{"CICADA-CHAIN", errorOnly, score, "a track's chain names no patterns or more than 32", "give the chain 1 to 32 pattern names", nil},
	{"CICADA-CLIP-RANGE", errorOnly, score, "an audio clip's region, gain or fades are invalid, or a clip is bound to a track that is not an audio track", "use start < end within the asset in frames, s or ms that resolve to whole frames, gain -60 to +24 dB, fades no longer than the region, and bind clips to audio tracks", nil},
	{"CICADA-DEMO", errorOnly, []string{"studio"}, "the public demo host refused its build or request (missing source commit, unsupported host, bad asset or entry document)", "build the demo from a full source commit and serve it from a supported host", nil},
	{"CICADA-DIRECTOR", errorOnly, []string{"score", "command"}, "the live block cannot become a Game Director manifest: an invalid state or stinger name, or a stinger pattern without a track slot", "use valid identifiers for states and stingers and bind each stinger pattern to a track slot", nil},
	{"CICADA-DUPLICATE", errorOnly, score, "a name is declared twice in one namespace, or uses a reserved name; the message names the first declaration", "rename or remove one of the declarations", nil},
	{"CICADA-EXPANSION", errorOnly, score, "a pattern expands beyond 64 steps after its phrase uses", "use fewer or shorter phrase uses, or split the pattern", nil},
	{"CICADA-EXPERIMENTAL", errorOnly, score, "an experimental voice (guitar) is used without opting in", "add experimental = on to the track", nil},
	{"CICADA-GRID", errorOnly, score, "a step grid attribute or tuplet group is invalid: tuplets need 2 to 8 pitches that divide a cell into whole ticks of at least 30", "use a supported step division and tuplet sizes", nil},
	{"CICADA-HISTORY", errorOnly, []string{"studio"}, "the public demo session has no change to undo or redo", "make a change first", nil},
	{"CICADA-IO", errorOnly, []string{"score", "command"}, "a file cannot be read, or has an extension the command does not accept", "check the path and pass a .cicada score (or .json where the command says so)", nil},
	{"CICADA-KEY", errorOnly, score, "the key root or scale is unknown", "use a root such as a, c# or eb and a scale: minor, major, dorian, phrygian, harmonic, mixo, pent or blues", nil},
	{"CICADA-LIB-ASSET", errorOnly, []string{"command"}, "convert cannot write library-owned audio into semantic JSON", "keep the score's imports, or vendor the library, until asset vendoring is supported", nil},
	{"CICADA-LIB-CAPABILITY", errorOnly, score, "an imported library requires an engine edition or capability this cicada does not support", "update cicada or use a library version that fits it", nil},
	{"CICADA-LIB-CYCLE", errorOnly, score, "libraries import each other in a cycle", "remove one import of the cycle the message names", nil},
	{"CICADA-LIB-DECL", errorOnly, score, "a library declares something only a score may declare, or a qualified name", "keep tracks, scenes, songs and other score-only declarations in the score; library names are unqualified", nil},
	{"CICADA-LIB-HASH", errorOnly, []string{"score", "command"}, "an imported library is not pinned in cicada.sum, or its content differs from the pin", "run cicada lib update <path> after an intended change; vendoring refuses a library that changed while it copied", nil},
	{"CICADA-LIB-IMPORT", errorOnly, score, "a score with imports was parsed without the project source loader", "check the score as a file (cicada check score.cicada) so imports resolve", nil},
	{"CICADA-LIB-MISSING", errorOnly, score, "an imported library is not found in std, the project lib/ folder or the user library", "run cicada lib list to see what resolves; create, vendor or fix the import path", nil},
	{"CICADA-LIB-PATH", errorOnly, []string{"score", "command"}, "a library path is invalid, or names std where a user library is required", "use a lowercase path such as demo/tone; std is reserved", nil},
	{"CICADA-LIB-PRIVATE", errorOnly, score, "a score references a library declaration that is private", "reference a public declaration of the library", nil},
	{"CICADA-LIB-REFERENCE", errorOnly, score, "a library reference does not name a declaration, uses a namespace no import binds, or a library parameter path lacks a direct import", "import the library and reference namespace.name of a public declaration", nil},
	{"CICADA-LIB-ROOT", errorOnly, []string{"command"}, "scores in different folders keep separate library pins", "run cicada lib update from each score's folder, or add a shared cicada.mod", nil},
	{"CICADA-LIB-SHADOW", errorOnly, score, "an import namespace collides with a declaration, another import or the reserved builtin namespace, or a library exists in more than one search location", "rename the import or the declaration, or remove the duplicate library", nil},
	{"CICADA-LIB-SOURCE", errorOnly, score, "a library's source file cannot be read", "restore the library's files or run cicada lib update", nil},
	{"CICADA-LIB-SUM", errorOnly, score, "cicada.sum is malformed", "let cicada lib update rewrite it; each line is path std|project|user sha256:<64 hex digits>", nil},
	{"CICADA-LIMIT", errorOnly, []string{"score", "studio"}, "a size or count limit is exceeded: name length, steps, tracks, voices, graph nodes, chain stages, image size, held inputs or source size", "reduce what the message counts; cicada check --profile browser lists the portable limits", nil},
	{"CICADA-LIVE-ATTACK", errorOnly, score, "a live layers attack other than 1bar is given", "use attack = 1bar", nil},
	{"CICADA-LIVE-BLOCK", errorOnly, score, "the live block is repeated, comes before a track, repeats a setting, or leaves an array implicit", "write one live block after all tracks with each setting once", nil},
	{"CICADA-LIVE-COUNT-IN", errorOnly, score, "the live count_in is not 0 to 8 whole bars", "use count_in = 0bars to 8bars", nil},
	{"CICADA-LIVE-LAND", errorOnly, score, "the live land value is unknown", "use land = now, beat, bar, 2bars, 4bars or phrase", nil},
	{"CICADA-LIVE-LIMIT", errorOnly, score, "the live block has more than 16 macros, or layers use more than 3 distinct thresholds", "remove macros or merge layer thresholds", nil},
	{"CICADA-LIVE-MACRO", errorOnly, score, "a live macro is duplicated, badly named, out of range, or maps to an invalid target", "give each macro a unique 1 to 64 byte name, a unitless value from 0 to 1 and valid targets", nil},
	{"CICADA-LIVE-PHRASE", errorOnly, score, "the live phrase is not 1 to 64 bars", "use phrase = 1bars to 64bars", nil},
	{"CICADA-LIVE-RECORD", errorOnly, score, "the live record division does not resolve to 30 to 3840 ticks", "use a musical division such as 1/16 or 1bar", nil},
	{"CICADA-LIVE-RELEASE", errorOnly, score, "the live layers release is not 1 to 16 bars", "use release = 1bars to 16bars", nil},
	{"CICADA-LIVE-TRACK", errorOnly, score, "a live layer rule names an unknown track or repeats one", "name each declared track at most once", nil},
	{"CICADA-LOCKED", errorOnly, []string{"edit"}, "a phrase repair would need to change a locked step", "unlock the step the message names, or edit the phrase directly", nil},
	{"CICADA-MANIFEST", errorOnly, score, "cicada.mod has an unknown, repeated or malformed directive", "keep one project name and one supported cicada edition, with each directive once", nil},
	{"CICADA-MIDI", errorOnly, score, "the midi block is invalid: a port is unknown or repeated, a channel is outside 1 to 16, a note or controller outside 0 to 127, or a mapping target is invalid", "declare each port once and map inputs to existing tracks, scenes and parameters", nil},
	{"CICADA-NOTE", errorOnly, []string{"studio"}, "a live note was refused: invalid input identity, note or velocity, or a voice without browser live notes", "send a MIDI note 0 to 127 with velocity 1 to 127 to a track whose voice plays live", nil},
	{"CICADA-NOTES", errorOnly, []string{"edit"}, "an editnotes batch was refused because of one note; the reason and the note (at, step, pitch or lane) come as data", "read reason and note in the error, then change the batch; docs/manual/editors.md describes each reason", noteReasons},
	{"CICADA-PARAM", errorOnly, []string{"score", "command", "studio"}, "a parameter, attribute or setting is unknown, out of range, or not allowed here", "check the name, range and unit with cicada params and cicada explain; the message names the value", nil},
	{"CICADA-PERFORMANCE", errorOnly, score, "a performance pattern is invalid: bad note fields, overlapping gates of one pitch, gesture times outside a note, or ticks outside a performance", "give each note a MIDI pitch, velocity 1 to 127, start and length in ticks inside the loop, and gesture points inside its held length", nil},
	{"CICADA-POSITION", errorOnly, score, "a position is invalid or out of order: arrangement tempo steps, automation points or contour points", "write positions as @bar.beat.step in increasing order inside the song; contours run from @0 to @1", nil},
	{"CICADA-PRESET-PARAM", errorOnly, score, "a preset sets a parameter its target instrument does not have, or a structural one", "set only the target's parameters (cicada explain lists them)", nil},
	{"CICADA-PRESET-RANGE", errorOnly, score, "a preset value is outside its parameter's range", "use a value inside the range the message gives", nil},
	{"CICADA-PRESET-TARGET", errorOnly, score, "a preset's instrument target is missing or incompatible", "set instrument = an existing instrument or built-in voice", nil},
	{"CICADA-PRESET-TYPE", errorOnly, score, "a preset value has the wrong type: not one of the allowed values, not on or off, not a finite number or not an integer", "use a value of the type the message expects", nil},
	{"CICADA-PRESET-UNIT", errorOnly, score, "a preset value has the wrong unit", "use the unit the message names", nil},
	{"CICADA-RATE", both, score, "a modulator's rate is not what it plays: the warning when loop = continue fits whole cycles to the song at another rate, the error when a routed modulator exceeds 20 Hz at the song's fastest tempo", "choose a rate that fits the song's length, or a slower one; the message gives the rate that plays", nil},
	{"CICADA-REFERENCE", errorOnly, score, "a name refers to nothing: an unknown track, pattern, scene, instrument, effect, bus, clip, asset or profile; close names get a spelling suggestion", "declare it, or use one of the names the message suggests", nil},
	{"CICADA-REVISION", errorOnly, []string{"studio"}, "the public demo session changed since the client read it", "refresh, then edit again", nil},
	{"CICADA-ROUTE", errorOnly, score, "a modulation route reaches a target it cannot modulate: a legacy effect setting, a non-track row, a non-continuous row, or an envelope's own input", "route to a continuous live track mixer or voice parameter", nil},
	{"CICADA-ROW", errorOnly, score, "a velocity row is invalid: wrong length, a value outside 1 to 127, or a holding dot with no earlier value", "give the row one entry per expanded step, values 1 to 127, and a value before the first dot", nil},
	{"CICADA-SAMPLER-MEMORY", errorOnly, []string{"score", "command"}, "the sampler packs decode to more PCM than the resident cap allows", "use smaller packs, or give a larger cap with --max-resident-pcm or CICADA_MAX_RESIDENT_PCM, such as 2GiB", nil},
	{"CICADA-SAMPLER-PACK", errorOnly, score, "a sampler pack is declared wrongly or does not load at 44.1, 48 or 96 kHz", "declare a relative JSON pack with its SHA-256 pin and no asset or loop mode, and fix the pack the message names", nil},
	{"CICADA-SAMPLER-PARAM", errorOnly, score, "a sampler's fields are invalid: root, mode, voices, envelope or the field set", "use an absolute root c0 to b6, mode oneshot or loop, voices 1 to 32 and valid envelope times", nil},
	{"CICADA-SAMPLER-ZONE", both, score, "a note falls outside every zone of its sampler pack, or a hit velocity no zone covers (errors); a warning when a hit's velocity is outside the recorded range and plays at the nearest recorded one", "move the note into the pack's key range, write it in another octave, change the pattern's transpose, or use a pack that covers it; for the warning, write a velocity the pack records", nil},
	{"CICADA-SCALE-DEGREE", errorOnly, score, "a scale degree does not exist in the score's scale (2 and 6 in pent and blues)", "choose a degree in the scale or use an absolute letter pitch", nil},
	{"CICADA-SEED", errorOnly, score, "a seed is outside 0 to 4294967295", "use a seed from 0 to 4294967295", nil},
	{"CICADA-SLIDE-REST", warningOnly, score, "a slide leads into a rest, so it has no effect", "slide into a note, or remove the slide", nil},
	{"CICADA-SOLO", warningOnly, score, "a saved solo switch is on, so render and check play only the soloed parts", "turn solo off before delivering, unless you mean it", nil},
	{"CICADA-SOURCE-MISSING", errorOnly, score, "a score that cicada.mod lists cannot be read", "restore the file or remove it from cicada.mod", nil},
	{"CICADA-SOURCE-PATH", errorOnly, score, "a source path is absolute, uses traversal or patterns, is not listed in cicada.mod, or leaves the project", "list project-relative .cicada paths in cicada.mod", nil},
	{"CICADA-SYNTAX", errorOnly, score, "the text does not parse: a token or declaration is malformed", "check braces, spelling and separators near the position the message gives", nil},
	{"CICADA-UNIT", errorOnly, score, "a value has the wrong unit or type for its field", "use the unit cicada explain gives for the field, such as Hz, ms, dB or bars", nil},
	{"CICADA-UNSUPPORTED", errorOnly, []string{"score", "command", "studio"}, "valid notation that the engine does not implement: a route, effect kind or placement, a setting without an engine representation, or an export option combination", "use a supported alternative; the message names what is not implemented", nil},
	{"CICADA-USE", errorOnly, score, "a phrase use is invalid: repeat outside 1 to 64 or transpose outside -24 to 24", "use repeat 1 to 64 and transpose -24 to +24", nil},
	{"CICADA-VERSION", errorOnly, []string{"score", "command", "studio"}, "the edition is unsupported, the score header and cicada.mod disagree, or a removed edition-1 spelling is used", "run cicada fix to migrate legacy spellings (check --json offers it as a fix); keep the header and cicada.mod on one edition", nil},
}

// noteReasons are the reasons a CICADA-NOTES refusal carries. They mirror
// edit.NoteErrorCodes; TestNotesReasonsMatchTheEditor keeps them in step.
var noteReasons = []Reason{
	{"invalid-note", "the note has a start, length, pitch or velocity outside its range"},
	{"note-changed", "the note is missing, or its length or velocity differs; read the pattern again"},
	{"no-lanes", "the batch names a lane in a pattern without drum lanes"},
	{"not-whole-steps", "the note does not start or end on the pattern's step grid"},
	{"outside-pattern", "the note would leave the pattern's steps"},
	{"outside-lanes", "the note would leave the pattern's lanes"},
	{"outside-loop", "the note would leave the performance loop"},
	{"pitch-range", "the note would leave MIDI 0 to 127, counting the pattern's transpose"},
	{"too-short", "the note would become shorter than one step or one tick"},
	{"gesture-cut", "the note would end before its last gesture point"},
	{"overlap", "the note would overlap another note: the same pitch in a performance, or any note in a step pattern"},
	{"same-pitch", "the note would land on the pitch of another selected note"},
	{"same-cell", "the note would land on the drum cell of another selected note"},
	{"chord-pitches-equal", "the note would equal another pitch of its own chord"},
	{"chord-size", "the note would make a chord of more than four pitches"},
	{"chord-articulation", "the note would make a chord with a note that slides or ratchets"},
	{"chord-length", "the note would join a chord of another length"},
	{"chord-modifiers", "the note would join a chord with another accent, chance or velocity"},
	{"acid-one-pitch", "the note would put a second pitch on an acid step"},
	{"fixed-length", "the note is a drum hit, which lasts one step"},
	{"unknown-lane", "the note names a drum lane the pattern does not have"},
}

// Catalog returns every code, sorted by code.
func Catalog() []Code {
	catalog := append([]Code(nil), codes...)
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Code < catalog[j].Code })
	return catalog
}

// Lookup returns one code's entry.
func Lookup(code string) (Code, bool) {
	for _, entry := range codes {
		if entry.Code == code {
			return entry, true
		}
	}
	return Code{}, false
}
