package project

import (
	"bytes"
	"encoding/json"
	"m31labs.dev/cicada/notation"
	"strings"
	"testing"
)

const chordSource = `tempo 120
key d minor
instrument piano { voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
track keys piano {}
pattern harmony notes { [d4 f4 a4]^?70 - . [c4 e4 g4] }
scene verse { keys=harmony }
song { verse }
`

func TestChordSourceJSONRoundTrip(t *testing.T) {
	score, ds := notation.Parse([]byte(chordSource))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	step := p.Patterns[0].Data[0]
	if len(step.Notes) != 3 || step.Notes[0] != 62 || step.Notes[1] != 65 || step.Notes[2] != 69 || !step.Accent || step.Probability != 70 {
		t.Fatalf("chord lost semantics: %+v", step)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ToSource(restored)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, ds := notation.Parse(source)
	if reparsed == nil {
		t.Fatalf("reparse: %+v\n%s", ds, source)
	}
	again, ds := FromScore(reparsed)
	if again == nil {
		t.Fatalf("recompile: %+v", ds)
	}
	got, _ := json.Marshal(again)
	if !bytes.Equal(data, got) {
		t.Fatalf("semantic roundtrip changed\n%s\n%s", data, got)
	}
	mono := strings.ReplaceAll(strings.ReplaceAll(chordSource, "voice poly", "voice mono"), "[d4 f4 a4]^?70 - . [c4 e4 g4]", "d4 - . c4")
	ms, ds := notation.Parse([]byte(mono))
	mp, ds := FromScore(ms)
	if mp == nil {
		t.Fatalf("mono: %+v", ds)
	}
	md, _ := json.Marshal(mp)
	if bytes.Contains(md, []byte(`"notes":`)) {
		t.Fatal("mono JSON acquired a notes field")
	}
}

func TestChordRejectsMalformedModesPitchesAndModifiers(t *testing.T) {
	for _, steps := range []string{"[d4]", "[]", "[d4 d4]", "[c4 d4 e4 f4 g4]", "[d4 f4]~", "[d4 f4]*2", "d4~ [f4 a4]"} {
		t.Run(steps, func(t *testing.T) {
			source := strings.ReplaceAll(chordSource, "[d4 f4 a4]^?70 - . [c4 e4 g4]", steps)
			score, _ := notation.Parse([]byte(source))
			if score == nil {
				return
			}
			p, _ := FromScore(score)
			if p != nil {
				t.Fatalf("malformed chord accepted: %s", steps)
			}
		})
	}
	for _, mode := range []string{"stereo", "mono"} {
		source := strings.ReplaceAll(chordSource, "voice poly", "voice "+mode)
		score, _ := notation.Parse([]byte(source))
		if score != nil {
			if p, _ := FromScore(score); p != nil {
				t.Fatalf("mode %s accepted chord", mode)
			}
		}
	}
}

func TestPolyphonyCountsAgainstGlobalVoiceBudget(t *testing.T) {
	for _, n := range []int{8, 9} {
		source := "instrument tone { voice poly { out=sine(pitch)*env(gate,100ms) } }\npattern p notes { [d4 f4] }\n"
		bindings := ""
		for i := 0; i < n; i++ {
			id := string(rune('a' + i))
			source += "track " + id + " tone {}\n"
			bindings += id + "=p "
		}
		source += "scene main { " + bindings + " } song { main }"
		score, _ := notation.Parse([]byte(source))
		p, ds := FromScore(score)
		if n == 8 && p == nil {
			t.Fatalf("32 voices rejected: %+v", ds)
		}
		if n == 9 && p != nil {
			t.Fatal("36 voices accepted")
		}
	}
}

func TestChordJSONUsesNumericArrayAndRejectsMalformedPayloads(t *testing.T) {
	score, _ := notation.Parse([]byte(chordSource))
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("source %+v", ds)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"notes":[62,65,69]`)) {
		t.Fatalf("notes did not serialize as numeric array: %s", data)
	}
	for _, bad := range []string{`[]`, `null`, `"PkFJ"`, `[62]`, `[62,62]`, `[62,128]`, `[62,-1]`, `[62,65,69,72,74]`} {
		candidate := bytes.Replace(data, []byte(`[62,65,69]`), []byte(bad), 1)
		if _, err := DecodeJSON(candidate); err == nil {
			t.Fatalf("malformed notes %s accepted", bad)
		}
	}
}

func TestUnusedChordSourceRoundTrip(t *testing.T) {
	source := strings.Replace(chordSource, "keys=harmony", "keys=off", 1)
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("unused chord: %+v", ds)
	}
	if ds := ValidateProject(p); ds != nil {
		t.Fatalf("validate: %+v", ds)
	}
	roundtrip, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	again, ds := notation.Parse(roundtrip)
	if again == nil {
		t.Fatalf("reparse: %+v", ds)
	}
	restored, ds := FromScore(again)
	if restored == nil {
		t.Fatalf("recompile: %+v", ds)
	}
	want, _ := json.Marshal(p)
	got, _ := json.Marshal(restored)
	if !bytes.Equal(want, got) {
		t.Fatalf("unused chord changed\n%s\n%s", want, got)
	}
}

func TestChordGrammarExtrasCompile(t *testing.T) {
	for _, chord := range []string{
		"[d4 // middle pitch\n f4 a4]^?70",
		"[d4 f4 a4]^\t?70",
		"[d4 f4 a4]^\n?70",
		"[d4 f4 a4]^ // chance\n ? 70",
		"[d4 // octave shift\n ' f4 a4]^?70",
	} {
		t.Run(chord, func(t *testing.T) {
			source := strings.Replace(chordSource, "[d4 f4 a4]^?70", chord, 1)
			score, ds := notation.Parse([]byte(source))
			if score == nil {
				t.Fatalf("parse: %+v", ds)
			}
			p, ds := FromScore(score)
			if p == nil {
				t.Fatalf("compile: %+v", ds)
			}
			step := p.Patterns[0].Data[0]
			if len(step.Notes) != 3 || !step.Accent || step.Probability != 70 {
				t.Fatalf("lost chord semantics: %+v", step)
			}
		})
	}
}

func TestChordGraphDelayChecksEveryPitch(t *testing.T) {
	source := strings.Replace(chordSource, "sine(pitch)", "comb(noise(), 1 / pitch, 0.9, 0.5)", 1)
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	p.Patterns[0].Data[0].Notes[1] = 0
	if err := ValidateProject(p); err == nil {
		t.Fatal("second chord pitch exceeded graph delay storage without rejection")
	}
	source = strings.Replace(source, "[d4 f4 a4]", "[d4 c0, a4]", 1)
	score, ds = notation.Parse([]byte(source))
	if score == nil {
		t.Fatal(ds)
	}
	if p, ds := FromScore(score); p != nil {
		t.Fatalf("source accepted chord with unbounded delay pitch: %+v", ds)
	}
}
