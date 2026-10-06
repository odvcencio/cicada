package notation

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSourcePrintAndFormat(t *testing.T) {
	source := []byte("cicada 1\r\n// Keep this comment\r\ntrack bass acid {}\r\npattern a acid steps=2 { 1^~*2%50 . }\r\nscene main { bass=a }\r\nsong { main }\r\n")
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Print(document), source) {
		t.Fatal("source printer changed bytes")
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(formatted), "// Keep this comment") || !strings.Contains(string(formatted), "1^~*2%50") {
		t.Fatalf("formatter lost authored content:\n%s", formatted)
	}
	if bytes.Contains(formatted, []byte("\r")) || !bytes.HasSuffix(formatted, []byte("\n")) {
		t.Fatalf("formatter did not normalize newlines:\n%s", formatted)
	}
	reparsed, ds := Parse(formatted)
	if reparsed == nil {
		t.Fatalf("formatted score did not parse: %+v\n%s", ds, formatted)
	}
	for _, diagnostic := range ds {
		if diagnostic.Severity == "error" {
			t.Fatalf("formatted score is invalid: %+v\n%s", ds, formatted)
		}
	}
	againDocument, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format(againDocument)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatted, again) {
		t.Fatalf("formatter is not idempotent:\n%s\n---\n%s", formatted, again)
	}
}

func TestFormatKeepsChanceAfterOctaveComma(t *testing.T) {
	source := []byte("track bass acid {}\npattern p acid steps=2 { 7,?70 . }\nscene main { bass=p }\nsong { main }\n")
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(formatted, []byte("7,?70 .")) {
		t.Fatalf("chance split from note: %s", formatted)
	}
	if _, diagnostics := Parse(formatted); len(diagnostics) != 0 {
		t.Fatalf("formatted chance diagnostics: %+v", diagnostics)
	}
}

func TestFormatPrintsSIUnits(t *testing.T) {
	source := []byte("track bass acid { cutoff = 2khz level = -6db }\npattern riff { 1 . }\nscene main { bass=riff }\nsong { main }\n")
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(formatted, []byte("2kHz")) || !bytes.Contains(formatted, []byte("-6dB")) {
		t.Fatalf("formatter did not use SI spelling: %s", formatted)
	}
	if _, diagnostics := Parse(formatted); len(diagnostics) != 0 {
		t.Fatalf("formatted source diagnostics: %+v", diagnostics)
	}
}

func TestFormatNamedMixerCatalogOrderAndComments(t *testing.T) {
	source := []byte(`fx drive drive {}
fx room delay {}
track bass acid {
  // output follows the sends in the catalog
  out = sfx
  // keep this with the delay send
  send room = 0.25 pre
  insert = drive
  solo = on
  mute = off
  pan = -0.5
  level = -9dB
}
bus music {
  solo = off
  insert = none
  level = -3dB
  mute = off
}
master { insert = none }
export web {
  normalize = off
  true_peak = -1dBTP
  loudness = -14LUFS
  tail = 1s
  bits = 24
  rate = 44100Hz
}
pattern p acid steps=1 { 1 }
scene main { bass=p }
song { main }
`)
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(formatted)
	ordered := []string{"level = -9dB", "pan = -0.5", "mute = off", "solo = on", "insert = drive", "send room = 0.25 pre", "out = sfx"}
	last := -1
	for _, setting := range ordered {
		index := strings.Index(text, setting)
		if index <= last {
			t.Fatalf("mixer setting %q is out of catalog order:\n%s", setting, formatted)
		}
		last = index
	}
	for _, comment := range []string{"// output follows the sends in the catalog", "// keep this with the delay send"} {
		if !strings.Contains(text, comment) {
			t.Fatalf("formatter lost comment %q:\n%s", comment, formatted)
		}
	}
	reparsed, diagnostics := Parse(formatted)
	if reparsed == nil {
		t.Fatalf("formatted mixer did not parse: %+v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("formatted mixer is invalid: %+v\n%s", diagnostics, formatted)
		}
	}
	again, err := Format(mustDocument(t, formatted))
	if err != nil || !bytes.Equal(formatted, again) {
		t.Fatalf("mixer formatter is not idempotent: %v\n%s\n---\n%s", err, formatted, again)
	}
}

func mustDocument(t *testing.T, source []byte) *Document {
	t.Helper()
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestFormatPatternBodySettings(t *testing.T) {
	source := []byte("track bass acid {}\npattern p { swing=56% gate=60% 1 . 3 . }\nscene main { bass=p }\nsong { main }\n")
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(formatted, []byte("pattern p {\n  swing = 56%\n  gate = 60%\n  1 . 3 .\n}")) {
		t.Fatalf("pattern settings are not separated from steps: %s", formatted)
	}
	if _, diagnostics := Parse(formatted); len(diagnostics) != 0 {
		t.Fatalf("formatted source diagnostics: %+v", diagnostics)
	}
}

func TestFormatSceneSettingsAfterBindingsWithComments(t *testing.T) {
	source := []byte("track bass acid { cutoff = 700Hz }\ntrack drums drums {}\nfx delay { feedback = 0.2 }\npattern riff acid steps=2 { 1 . }\npattern beat drums { bd: x. }\nscene main {\n  bass.cutoff = 900Hz // cutoff scene setting\n  drums.bd_level = off\n  bass = riff // pattern binding\n  delay.feedback = 0.3 // delay scene setting\n}\nsong { main }\n")
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(formatted)
	positions := []string{"bass = riff", "bass.cutoff = 900Hz", "drums.bd_level = off", "delay.feedback = 0.3"}
	last := -1
	for _, fragment := range positions {
		index := strings.Index(text, fragment)
		if index <= last {
			t.Fatalf("scene binding/settings order is wrong at %q:\n%s", fragment, text)
		}
		last = index
	}
	for _, comment := range []string{"// cutoff scene setting", "// pattern binding", "// delay scene setting"} {
		if !strings.Contains(text, comment) {
			t.Fatalf("formatter lost %q:\n%s", comment, text)
		}
	}
	score, diagnostics := Parse(formatted)
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("formatted scene settings do not parse: %+v\n%s", diagnostics, text)
	}
	if got := score.Scenes[0]; got.Bindings[0].Track != "bass" || got.Bindings[0].Pattern != "riff" || len(got.Settings) != 3 || got.Settings[0].Path != "bass.cutoff" || got.Settings[1].Path != "drums.bd_level" || got.Settings[2].Path != "delay.feedback" {
		t.Fatalf("scene settings did not round trip in source order: %+v", got)
	}
	againDoc, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format(againDoc)
	if err != nil || !bytes.Equal(formatted, again) {
		t.Fatalf("scene formatting is not idempotent: %v\n%s", err, again)
	}
}

func TestFormatKeepsPhraseUseTranspose(t *testing.T) {
	source := []byte("pattern p acid { use hook transpose = 12 }\n")
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(formatted, []byte("use hook transpose = 12")) {
		t.Fatalf("phrase transpose was split: %s", formatted)
	}
	if _, err := ParseDocument(formatted); err != nil {
		t.Fatalf("formatted source syntax: %v", err)
	}
}

func TestFormatGroupsDrumHitsByFourCells(t *testing.T) {
	source := []byte("track drums drums {}\npattern beat drums { bd: x.x*2.x?50.X. }\nscene main { drums=beat }\nsong { main }\n")
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(formatted, []byte("bd: x.x*2. x?50.X.")) {
		t.Fatalf("drum row is not grouped by beat: %s", formatted)
	}
	if _, diagnostics := Parse(formatted); len(diagnostics) != 0 {
		t.Fatalf("formatted score diagnostics: %+v", diagnostics)
	}
	again, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	reformatted, err := Format(again)
	if err != nil || !bytes.Equal(formatted, reformatted) {
		t.Fatalf("drum grouping is not idempotent: %v\n%s", err, reformatted)
	}
}

func TestFormatPreservesCommentInsideDrumRow(t *testing.T) {
	source := []byte("track drums drums {}\npattern beat drums { bd: x.. // pickup\n x.x.x }\nscene main { drums=beat }\nsong { main }\n")
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(formatted, []byte("// pickup")) {
		t.Fatalf("formatter discarded a drum-row comment: %s", formatted)
	}
	if !bytes.Contains(formatted, []byte("bd: x..\n  // pickup\n      x .x.x")) {
		t.Fatalf("commented drum row lost its beat boundary: %s", formatted)
	}
	if _, diagnostics := Parse(formatted); len(diagnostics) != 0 {
		t.Fatalf("formatted score diagnostics: %+v\n%s", diagnostics, formatted)
	}
	again, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	reformatted, err := Format(again)
	if err != nil || !bytes.Equal(formatted, reformatted) {
		t.Fatalf("commented drum grouping is not idempotent: %v\n%s", err, reformatted)
	}
}

func TestFormatKeepsOctaveMarksAndGroupingParens(t *testing.T) {
	source := []byte("cicada 1\ninstrument sub {\n  voice mono {\n    let shape = env(gate, 90ms);\n    out = saw(pitch)*( shape * velocity );\n  }\n}\ntrack low sub {}\npattern a notes steps=4 { 7,~ 5,,^*2 3, ~%50 c2, }\nscene main { low=a }\nsong { main }\n")
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"out = saw(pitch) * (shape * velocity)",
		"pattern a notes steps = 4 {\n  7,~ 5,,^*2 3,~%50 c2,\n}",
	} {
		if !strings.Contains(string(formatted), want) {
			t.Fatalf("formatted score lacks %q:\n%s", want, formatted)
		}
	}
	original, ds := Parse(source)
	if len(ds) != 0 {
		t.Fatalf("source diagnostics: %+v", ds)
	}
	reparsed, ds := Parse(formatted)
	if len(ds) != 0 {
		t.Fatalf("formatted diagnostics: %+v\n%s", ds, formatted)
	}
	for i, step := range original.Patterns[0].Steps {
		if got := reparsed.Patterns[0].Steps[i].Text; got != strings.ReplaceAll(step.Text, " ", "") {
			t.Fatalf("step %d = %q, want %q", i, got, step.Text)
		}
	}
}

func TestFormatKeepsAcidOctaveCommasAttached(t *testing.T) {
	source := []byte("cicada 1\ntrack bass acid {}\npattern a acid steps=4 { 7,^ 7,,~ 7, . }\nscene main { bass=a }\nsong { main }\n")
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(formatted, []byte("7,^ 7,,~ 7, .")) {
		t.Fatalf("formatter split octave modifiers:\n%s", formatted)
	}
	if _, diagnostics := Parse(formatted); len(diagnostics) != 0 {
		t.Fatalf("formatted score diagnostics: %+v\n%s", diagnostics, formatted)
	}
}

func TestExamplesRemainValidAfterFormat(t *testing.T) {
	for _, name := range []string{"first-acid", "glassbass", "circuit-kit", "acid-voice", "cicada-chorus"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile("../examples/" + name + ".cicada")
			if err != nil {
				t.Fatal(err)
			}
			document, err := ParseDocument(source)
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := Format(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, ds := Parse(formatted); len(ds) != 0 {
				t.Fatalf("formatted score diagnostics: %+v\n%s", ds, formatted)
			}
			againDocument, err := ParseDocument(formatted)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Format(againDocument)
			if err != nil || !bytes.Equal(formatted, again) {
				t.Fatalf("format changed on second pass: %v", err)
			}
		})
	}
}

func TestGraphPluckExampleIsFormatted(t *testing.T) {
	source, err := os.ReadFile("../examples/pluck.cicada")
	if err != nil {
		t.Fatal(err)
	}
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, formatted) {
		t.Fatal("pluck example needs formatting")
	}
}
