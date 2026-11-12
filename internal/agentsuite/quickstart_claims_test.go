package agentsuite

import (
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The checks below try, against the binary, each claim the quick start makes
// beside its loop: the envelope's entity forms and declaration types, the
// refusal fields, the score notation, and how a voice's controls are found.
// A claim the page drops can be dropped here; a claim the binary stops
// keeping fails here.

// envelopeClaims tries the entity forms and the declaration types the page
// lists, on the project's score at revision, and returns the new revision.
func (q *quickStart) envelopeClaims(revision string) string {
	q.t.Helper()
	q.uses("entity", "param:<track>.<control>", "pattern:<name>", "step:<pattern>/<index>", "step:<pattern>/<lane>/<index>", "replacedeclaration", "declaration", "pattern", "track_decl", "scene_decl", "song_decl", "patchtext", "hunks", "line", "old", "new", "replacetext", "source")
	var catalog struct {
		Intents []struct {
			Example map[string]any `json:"example"`
			Fields  []struct {
				Name   string   `json:"name"`
				Values []string `json:"values"`
			} `json:"fields"`
		} `json:"intents"`
	}
	stdout, code := q.run("intents", "--json", "replacedeclaration")
	decode(q.t, "intents --json replacedeclaration", stdout, &catalog)
	listed := map[string]bool{}
	for _, field := range catalog.Intents[0].Fields {
		if field.Name == "declaration" {
			for _, value := range field.Values {
				listed[value] = true
			}
		}
	}
	for _, kind := range []string{"pattern", "track_decl", "scene_decl", "song_decl"} {
		if code != 0 || !listed[kind] {
			q.t.Fatalf("intents --json replacedeclaration: declaration values %v lack %s", listed, kind)
		}
	}
	replace := func(declaration, name, text string) map[string]any {
		intent := map[string]any{"kind": "replacedeclaration", "declaration": declaration, "text": text}
		if name != "" {
			intent["name"] = name
		}
		return intent
	}
	revision = q.edit("replacedeclaration track_decl", revision, replace("track_decl", "bass", "track bass acid { level = 6dB cutoff = 800Hz }"))
	revision = q.edit("replacedeclaration scene_decl", revision, replace("scene_decl", "verse", "scene verse {\n  drums = beat\n  bass = line\n  keys = chords\n}"))
	revision = q.edit("replacedeclaration song_decl", revision, replace("song_decl", "", "song { verse*2 drop*2 verse*4 }"))
	revision = q.edit("replacetext with the whole score as source", revision, map[string]any{"kind": "replacetext", "source": strings.Replace(string(q.read("main.cicada")), "tempo 120", "tempo 118", 1)})
	if !strings.Contains(string(q.read("main.cicada")), "tempo 118") {
		q.t.Fatal("replacetext did not write its source")
	}
	// patchtext replaces whole lines, named by line (from 1), old and new.
	score := string(q.read("main.cicada"))
	at := strings.LastIndex(score[:strings.Index(score, "tempo 118")], "\n") + 1
	end := at + strings.Index(score[at:], "\n") + 1
	revision = q.edit("patchtext with one hunk", revision, map[string]any{"kind": "patchtext", "hunks": []any{map[string]any{"line": strings.Count(score[:at], "\n") + 1, "old": score[at:end], "new": strings.Replace(score[at:end], "tempo 118", "tempo 116", 1)}}})
	if got := string(q.read("main.cicada")); got != strings.Replace(score, "tempo 118", "tempo 116", 1) {
		q.t.Fatalf("patchtext wrote %q", got)
	}

	var patternSettings struct {
		Intents []struct {
			Example map[string]any `json:"example"`
		} `json:"intents"`
	}
	stdout, code = q.run("intents", "--json", "setpatternsettings")
	decode(q.t, "intents --json setpatternsettings", stdout, &patternSettings)
	settings := patternSettings.Intents[0].Example
	if code != 0 || settings == nil {
		q.t.Fatalf("intents --json setpatternsettings: exit %d", code)
	}
	settings["entity"] = "pattern:line"
	revision = q.edit("an intent on pattern:<name>", revision, settings)
	return q.edit("a drum step, step:<pattern>/<lane>/<index>", revision, map[string]any{"kind": "togglestep", "entity": "step:beat/bd/1"})
}

// mistakeThree makes the page's third mistake twice, a guessed pattern and a
// guessed control, with the refusals the page quotes and its way out.
func (q *quickStart) mistakeThree(revision string) string {
	q.t.Helper()
	q.uses("cicada intents --json --index", "cicada intents --json <kind>", "intent_refused", "error.index", "error.kind", "error.field", `unknown pattern "lines"`, "symbols --json", `track keys does not expose sound parameter "cutoff"`)
	var index struct {
		Schema string `json:"schema"`
		Index  []struct {
			Kind    string `json:"kind"`
			Meaning string `json:"meaning"`
		} `json:"index"`
	}
	stdout, code := q.run("intents", "--json", "--index")
	decode(q.t, "intents --json --index", stdout, &index)
	kinds := map[string]bool{}
	for _, entry := range index.Index {
		kinds[entry.Kind] = entry.Meaning != ""
	}
	if code != 0 || index.Schema != "cicada.intents_index.v1" || !kinds["togglestep"] || !kinds["setparam"] {
		q.t.Fatalf("intents --json --index: exit %d, schema %q, %d kinds", code, index.Schema, len(index.Index))
	}
	var toggle struct {
		Intents []struct {
			Example map[string]any `json:"example"`
		} `json:"intents"`
	}
	stdout, code = q.run("intents", "--json", "togglestep")
	decode(q.t, "intents --json togglestep", stdout, &toggle)
	if code != 0 || len(toggle.Intents) != 1 || toggle.Intents[0].Example == nil {
		q.t.Fatalf("intents --json togglestep: exit %d", code)
	}
	guess := toggle.Intents[0].Example
	guess["entity"] = "step:lines/3"
	refused, code := q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{guess}}, true)
	if code != 1 || refused.Error == nil || refused.Error.Code != "intent_refused" || refused.Error.Index == nil || *refused.Error.Index != 0 || refused.Error.Kind != "togglestep" || refused.Error.Field != "entity" || refused.Error.Message != `unknown pattern "lines"` {
		q.t.Fatalf("a guessed pattern: exit %d %+v", code, refused.Error)
	}
	var symbols []struct {
		Role string `json:"role"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	stdout, code = q.run("symbols", "--json", "main.cicada")
	decode(q.t, "symbols --json", stdout, &symbols)
	pattern := ""
	for _, symbol := range symbols {
		if symbol.Role == "definition" && symbol.Kind == "pattern" && strings.HasPrefix("lines", symbol.Name) {
			pattern = symbol.Name
		}
	}
	if code != 0 || pattern != "line" {
		q.t.Fatalf("symbols --json: exit %d, no pattern line in %+v", code, symbols)
	}
	guess["entity"] = "step:" + pattern + "/3"
	revision = q.edit("the corrected step", revision, guess)
	refused, code = q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{map[string]any{"kind": "setparam", "entity": "param:keys.cutoff", "value": "900Hz"}}}, true)
	if code != 1 || refused.Error == nil || refused.Error.Code != "intent_refused" || refused.Error.Message != `track keys does not expose sound parameter "cutoff"` {
		q.t.Fatalf("a guessed control: exit %d %+v", code, refused.Error)
	}
	return revision
}

// refusalClaims tries the refusal fields the page names: the apply error's
// reason, an invalid result, the check fields and fix, and a missed loudness
// target with no remedy.
func (q *quickStart) refusalClaims(revision string) {
	q.t.Helper()
	q.uses("error", "code", "message", "error.reason", "invalid_result", "scores[].diagnostics", "start.line", "start.column", "hint", "fix", "intents", "fix.revision", "error.code", "remedy", "loudness.integrated_lufs", "true_peak_dbtp")
	refused, code := q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{map[string]any{"kind": "setparam", "entity": "param:bass.level", "value": "20dB"}}}, true)
	if code != 1 || refused.Error == nil || refused.Error.Code != "intent_refused" || refused.Error.Field != "value" || refused.Error.Reason == "" || refused.Error.Message == "" {
		q.t.Fatalf("a level out of range: exit %d %+v", code, refused.Error)
	}
	refused, code = q.apply(map[string]any{"version": 1, "revision": revision, "intents": []any{map[string]any{"kind": "replacedeclaration", "declaration": "scene_decl", "name": "verse", "text": "scene verse { drums = beat bass = lines }"}}}, false)
	if code != 1 || refused.Error == nil || refused.Error.Code != "invalid_result" {
		q.t.Fatalf("a batch whose result does not check: exit %d %+v", code, refused.Error)
	}

	// A missed loudness target with no remedy: aim at what it reached.
	missed, code := q.render("--loudness", "-6")
	if code != 1 || missed.OK || missed.Error == nil || missed.Error.Remedy != nil || missed.Loudness == nil || missed.Loudness.IntegratedLUFS == nil || missed.Loudness.TruePeakDBTP == nil {
		q.t.Fatalf("render to -6 LUFS: exit %d %+v %+v", code, missed.Error, missed.Loudness)
	}
	reached := strconv.FormatFloat(math.Floor(*missed.Loudness.IntegratedLUFS*10)/10, 'f', 1, 64)
	if aimed, code := q.render("--loudness", reached); code != 0 || !aimed.OK || !aimed.Loudness.TargetMet {
		q.t.Fatalf("render at the reached %s LUFS: exit %d %+v", reached, code, aimed.Error)
	}

	// The check fields, on two loose scores beside the project.
	q.in(filepath.Join(q.root, "refusals"), func() {
		q.write("ref.cicada", []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass = rif }\nsong { main*2 }\n"))
		check, code := q.checkScore("ref.cicada")
		if code != 1 || check.Valid || check.Scores[0].Valid || len(check.Scores[0].Diagnostics) == 0 {
			q.t.Fatalf("check of an unknown pattern: exit %d %+v", code, check)
		}
		diagnostic := check.Scores[0].Diagnostics[0]
		if diagnostic.Code == "" || diagnostic.Message == "" || diagnostic.Start == nil || diagnostic.Start.Line != 5 || diagnostic.Start.Column < 1 || diagnostic.Hint == "" {
			q.t.Fatalf("check diagnostic: %+v", diagnostic)
		}
		q.write("snap.cicada", []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern pulse { a1 . . . e2 . . . }\narrange {\n  place intro bass pulse { at = 100ticks length = 1bars }\n}\n"))
		check, code = q.checkScore("snap.cicada")
		if code != 1 || len(check.Scores[0].Diagnostics) != 1 || check.Scores[0].Diagnostics[0].Fix == nil {
			q.t.Fatalf("check of an off-step placement: exit %d %+v", code, check)
		}
		fix := check.Scores[0].Diagnostics[0].Fix
		if len(fix.Intents) == 0 || fix.Revision != check.Scores[0].Revision {
			q.t.Fatalf("fix: %+v, score revision %s", fix, check.Scores[0].Revision)
		}
		if result, code := q.applyTo("snap.cicada", map[string]any{"version": 1, "revision": fix.Revision, "intents": anySlice(fix.Intents)}, true); code != 0 || !result.Written {
			q.t.Fatalf("apply of the fix: exit %d %+v", code, result.Error)
		}
		if check, code := q.checkScore("snap.cicada"); code != 0 || !check.Valid {
			q.t.Fatalf("check after the fix: exit %d %+v", code, check)
		}
	})
}

// scoreClaims tries the notation the page teaches, on loose scores beside the
// project: pitches by `cicada events`, ties by the MIDI export, lengths and
// drum rows by check, scenes and song lengths by audition, and the edition
// line by an edit.
func (q *quickStart) scoreClaims() {
	q.t.Helper()
	q.uses("cicada 2", "track <name> <voice> { settings }", ".", "-", "|", "1", "7", "'", ",", "octave", "a1", "c#3", "[1 3 5]", "acid", "piano", "bd sd ch oh cp rs lt mt ht cb cy", "x", "X", "stop", "song", "verse*4", "verse")
	q.says("16 steps fill one bar")
	q.says("1 to 64 steps")
	q.says("C4 is MIDI 60")
	q.in(filepath.Join(q.root, "notation"), func() {
		lanes := strings.Fields("bd sd ch oh cp rs lt mt ht cb cy")
		var rows strings.Builder
		for _, lane := range lanes {
			rows.WriteString("  " + lane + ": x... X... .... ....\n")
		}
		q.write("notes.cicada", []byte("cicada 2\nkey a minor\n"+
			"track k piano {}\ntrack hi piano { octave = 3 }\ntrack b acid {}\ntrack d drums {}\ntrack l piano {}\n"+
			"pattern p { 1 1' 1, a1 | c#3 [1 3 5] . . }\npattern p3 { 1 . . . }\npattern ac { [1 3 5] . . . }\n"+
			"pattern long { "+strings.Repeat("1 ", 64)+"}\n"+
			"pattern kit drums {\n"+rows.String()+"}\n"+
			"scene main { k = p hi = p3 b = ac d = kit l = long }\nsong { main }\n"))
		if check, code := q.checkScore("notes.cicada"); code != 0 || !check.Valid {
			q.t.Fatalf("the notation the page teaches does not check: exit %d %+v", code, check)
		}
		// 65 steps, and drum rows of two lengths, are refused.
		q.write("long.cicada", []byte("cicada 2\ntrack l piano {}\npattern long { "+strings.Repeat("1 ", 65)+"}\nscene main { l = long }\nsong { main }\n"))
		q.write("rows.cicada", []byte("cicada 2\ntrack d drums {}\npattern kit drums {\n  bd: x... x...\n  sd: x...\n}\nscene main { d = kit }\nsong { main }\n"))
		for _, score := range []string{"long.cicada", "rows.cicada"} {
			if check, code := q.checkScore(score); code != 1 || check.Valid {
				q.t.Fatalf("%s checks, the page says it cannot", score)
			}
		}
		type event struct {
			Tick      int   `json:"Tick"`
			Note      int   `json:"Note"`
			Notes     []int `json:"Notes"`
			NoteCount int   `json:"NoteCount"`
		}
		events := func(track, pattern string) []event {
			var list []event
			stdout, code := q.run("events", "notes.cicada", track, pattern)
			decode(q.t, "events", stdout, &list)
			if code != 0 {
				q.t.Fatalf("events %s %s: exit %d", track, pattern, code)
			}
			return list
		}
		// One bar of the 8-step pattern: degrees in octave 2, ' and , move an
		// octave, letters are absolute, | adds no step, brackets are a chord,
		// and the pattern repeats after 8 steps of 240 ticks (16 to the bar).
		const step = 240
		want := []event{{Tick: 0, Note: 45}, {Tick: step, Note: 57}, {Tick: 2 * step, Note: 33}, {Tick: 3 * step, Note: 33}, {Tick: 4 * step, Note: 49}, {Tick: 5 * step, NoteCount: 3, Notes: []int{45, 48, 52}}}
		got := events("k", "p")
		if len(got) != 2*len(want) {
			q.t.Fatalf("events k p: %d events in a bar, want two passes of %d", len(got), len(want))
		}
		for i, w := range want {
			for pass, g := range []event{got[i], got[i+len(want)]} {
				ok := g.Tick == w.Tick+pass*8*step
				if w.NoteCount > 0 {
					ok = ok && g.NoteCount == 3 && g.Notes[0] == 45 && g.Notes[1] == 48 && g.Notes[2] == 52
				} else {
					ok = ok && g.Note == w.Note
				}
				if !ok {
					q.t.Fatalf("events k p, step %d pass %d: %+v, want %+v", i, pass, g, w)
				}
			}
		}
		if got := events("hi", "p3"); len(got) == 0 || got[0].Note != 57 {
			q.t.Fatalf("events with octave = 3: %+v", got)
		}
		if got := events("b", "ac"); len(got) < 3 || got[2].Tick >= step || got[0].NoteCount > 1 {
			q.t.Fatalf("a chord on acid: %+v, want three notes inside one step", got)
		}

		// - holds the note before it: the tied note is longer in the MIDI file.
		lengths := map[string]int{}
		for name, steps := range map[string]string{"tied": "1 - - .", "single": "1 . . ."} {
			q.write(name+".cicada", []byte("cicada 2\nkey a minor\ntrack k piano {}\npattern p { "+steps+" }\nscene main { k = p }\nsong { main }\n"))
			if _, code := q.run("midi", name+".cicada", "-o", name+".mid"); code != 0 {
				q.t.Fatalf("midi %s: exit %d", name, code)
			}
			notes := midiNoteLengths(q.t, q.read(name+".mid"))
			if len(notes) == 0 {
				q.t.Fatalf("%s.mid has no notes", name)
			}
			lengths[name] = notes[0]
		}
		if lengths["tied"] < 2*lengths["single"] {
			q.t.Fatalf("a note held by - lasts %d ticks, an untied one %d", lengths["tied"], lengths["single"])
		}

		// A scene's stop silences a track (under -80 dBFS, where audition
		// calls a track silent), and verse*3 is three bars.
		q.write("form.cicada", []byte("cicada 2\ntempo 120\nkey a minor\ntrack k piano {}\npattern p { [1 3 5] . . . }\nscene verse { k = p }\nscene rest { k = stop }\nsong { verse*3 rest*1 }\n"))
		var report struct {
			Score struct {
				SongBars int `json:"song_bars"`
			} `json:"score"`
			Bars []struct {
				MixRMS float64 `json:"mix_rms_dbfs"`
			} `json:"bars"`
		}
		stdout, code := q.run("audition", "form.cicada", "--json")
		decode(q.t, "audition form.cicada", stdout, &report)
		if code != 0 || report.Score.SongBars != 4 || len(report.Bars) != 4 || report.Bars[0].MixRMS <= -80 || report.Bars[3].MixRMS > -80 {
			q.t.Fatalf("verse*3 rest*1 with stop: exit %d %+v", code, report)
		}

		// Without cicada 2, a loose score is edition 1 and a mixer edit is
		// refused; with it, the same edit applies.
		for _, header := range []string{"", "cicada 2\n"} {
			q.write("edition.cicada", []byte(header+"tempo 120\ntrack bass acid {}\npattern riff { a1 . }\nscene main { bass = riff }\nsong { main }\n"))
			result, code := q.applyTo("edition.cicada", map[string]any{"version": 1, "intents": []any{map[string]any{"kind": "setparam", "entity": "param:bass.level", "value": "-3dB"}}}, false)
			refused := code == 1 && result.Error != nil && result.Error.Code == "intent_refused" && result.Error.Reason != ""
			if header == "" && !refused || header != "" && (code != 0 || !result.OK) {
				q.t.Fatalf("a mixer edit with header %q: exit %d %+v", header, code, result.Error)
			}
		}
	})
}

// voiceClaims tries the voices the page names and the way it gives to find
// their controls: each voice checks; cicada params has controls under the
// engine the page names for it; for every one of them, cicada explain fails
// exactly when the track lacks it (setparam then refuses it, and when
// explain succeeds the track has it); and level and pan fit every track.
func (q *quickStart) voiceClaims() {
	q.t.Helper()
	q.uses("cicada params", "path", "voices", "cicada explain", "level", "pan")
	var voices []string
	for _, match := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(q.line("`track <name> <voice> { settings }`"), -1) {
		voices = append(voices, match[1])
	}
	engines := map[string]string{}
	for _, match := range regexp.MustCompile("`([a-z_]+)`\\s+is\\s+`([a-z_]+)`").FindAllStringSubmatch(q.line("Guessing a name"), -1) {
		engines[match[1]] = match[2]
	}
	if len(voices) < 5 || len(engines) == 0 {
		q.t.Fatalf("voices %v, engines %v", voices, engines)
	}
	var params []struct {
		ID      string   `json:"id"`
		Path    string   `json:"path"`
		Voices  []string `json:"voices"`
		Unit    string   `json:"unit"`
		Default *float64 `json:"default"`
	}
	stdout, code := q.run("params")
	decode(q.t, "params", stdout, &params)
	if code != 0 {
		q.t.Fatalf("params: exit %d", code)
	}
	q.in(filepath.Join(q.root, "voices"), func() {
		var score strings.Builder
		score.WriteString("cicada 2\n")
		for i, voice := range voices {
			fmt.Fprintf(&score, "track t%d %s {}\n", i, voice)
		}
		fmt.Fprintf(&score, "pattern beat drums { bd: x... }\nscene main { %s = beat }\nsong { main }\n", drumTrack(voices))
		q.write("voices.cicada", []byte(score.String()))
		if check, code := q.checkScore("voices.cicada"); code != 0 || !check.Valid {
			q.t.Fatalf("a score with the voices %v does not check: exit %d %+v", voices, code, check)
		}
		setparam := func(track, path string, value any) quickApply {
			result, _ := q.applyTo("voices.cicada", map[string]any{"version": 1, "intents": []any{map[string]any{"kind": "setparam", "entity": "param:" + track + "." + path, "value": value}}}, false)
			return result
		}
		for i, voice := range voices {
			track := fmt.Sprintf("t%d", i)
			engine := engines[voice]
			if engine == "" {
				engine = voice
			}
			listed := 0
			for _, param := range params {
				if strings.HasPrefix(param.ID, "mix.") || !contains(param.Voices, engine) {
					continue
				}
				listed++
				value := any(0.0)
				if param.Default != nil {
					value = *param.Default
					switch param.Unit {
					case "Hz", "ms", "dB":
						value = strconv.FormatFloat(*param.Default, 'f', -1, 64) + param.Unit
					}
				}
				_, explainCode := q.run("explain", "voices.cicada", track+"."+param.Path)
				result := setparam(track, param.Path, value)
				unknown := result.Error != nil && strings.Contains(result.Error.Message, "unknown parameter")
				if explainCode != 0 && result.OK || explainCode == 0 && unknown {
					q.t.Errorf("%s on a %s track: explain exits %d, setparam %+v", param.Path, voice, explainCode, result.Error)
				}
			}
			if listed == 0 {
				q.t.Errorf("cicada params has no control under engine %s, the page's way to the controls of %s", engine, voice)
			}
			for _, path := range []string{"level", "pan"} {
				if result := setparam(track, path, 0); !result.OK {
					q.t.Errorf("a %s track refuses %s: %+v", voice, path, result.Error)
				}
			}
		}
		if _, code := q.run("explain", "voices.cicada", drumTrack(voices)+".cutoff"); code == 0 {
			q.t.Error("cicada explain accepts a control the track lacks")
		}
	})
}

// drumTrack names the track of the voices score that plays drums.
func drumTrack(voices []string) string {
	for i, voice := range voices {
		if voice == "drums" {
			return fmt.Sprintf("t%d", i)
		}
	}
	return "t0"
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// midiNoteLengths returns, in order of their starts, the length in ticks of
// every note in a Standard MIDI File.
func midiNoteLengths(t interface {
	Helper()
	Fatalf(string, ...any)
}, data []byte) []int {
	t.Helper()
	var lengths []int
	for len(data) >= 8 {
		kind, size := string(data[:4]), int(binary.BigEndian.Uint32(data[4:8]))
		if 8+size > len(data) {
			t.Fatalf("truncated MIDI chunk %q", kind)
		}
		chunk := data[8 : 8+size]
		data = data[8+size:]
		if kind != "MTrk" {
			continue
		}
		started := map[byte]int{}
		order := map[byte]int{}
		tick, status := 0, byte(0)
		for i := 0; i < len(chunk); {
			delta := 0
			for i < len(chunk) {
				b := chunk[i]
				i++
				delta = delta<<7 | int(b&0x7f)
				if b&0x80 == 0 {
					break
				}
			}
			tick += delta
			if chunk[i]&0x80 != 0 {
				status = chunk[i]
				i++
			}
			switch {
			case status == 0xff:
				i++ // meta type
				size := 0
				for chunk[i]&0x80 != 0 {
					size = size<<7 | int(chunk[i]&0x7f)
					i++
				}
				size = size<<7 | int(chunk[i])
				i += 1 + size
			case status == 0xf0 || status == 0xf7:
				size := int(chunk[i])
				i += 1 + size
			case status&0xf0 == 0x90 && chunk[i+1] > 0:
				started[chunk[i]] = tick
				order[chunk[i]] = len(lengths)
				lengths = append(lengths, -1)
				i += 2
			case status&0xf0 == 0x80 || status&0xf0 == 0x90:
				if at, ok := started[chunk[i]]; ok {
					lengths[order[chunk[i]]] = tick - at
					delete(started, chunk[i])
				}
				i += 2
			case status&0xf0 == 0xc0 || status&0xf0 == 0xd0:
				i++
			default:
				i += 2
			}
		}
	}
	return lengths
}
