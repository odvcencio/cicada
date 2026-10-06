package project

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const expressionTestSource = `track lead acid {}
pattern take {
  c4 - - .
  bend: +50ct . -1200ct 0ct
  vibrato: . 4ct 12ct 0ct
  pressure: 0 . 0.7 0
  timbre: . 0.8 . 0.5
}
scene main { lead = take }
song { main }
`

func expressionTestProject(t *testing.T) *Project {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(expressionTestSource))
	if len(diagnostics) != 0 {
		t.Fatalf("source: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	return p
}

func TestExpressionHoldsCompileAndRoundTrip(t *testing.T) {
	p := expressionTestProject(t)
	want := []NoteExpression{
		{PitchCents: 50, Timbre: 0.5},
		{PitchCents: 50, Timbre: 0.8, VibratoDepthCents: 4},
		{PitchCents: -1200, Pressure: 0.7, Timbre: 0.8, VibratoDepthCents: 12},
		{Timbre: 0.5},
	}
	if !reflect.DeepEqual(p.Patterns[0].Expression, want) {
		t.Fatalf("resolved expression: %+v", p.Patterns[0].Expression)
	}
	jsonSource, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(jsonSource)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Patterns[0].Expression, want) {
		t.Fatalf("JSON expression changed: %+v", decoded.Patterns[0].Expression)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"bend:", "vibrato:", "pressure:", "timbre:"} {
		if !strings.Contains(string(rewritten), row) {
			t.Fatalf("missing %s in rewritten source: %s", row, rewritten)
		}
	}
	score, diagnostics := notation.Parse(rewritten)
	if len(diagnostics) != 0 {
		t.Fatalf("rewritten parse: %+v", diagnostics)
	}
	converted, diagnostics := FromScore(score)
	if converted == nil {
		t.Fatalf("rewritten compile: %+v", diagnostics)
	}
	convertedJSON, err := CanonicalJSON(converted)
	if err != nil || !bytes.Equal(jsonSource, convertedJSON) {
		t.Fatalf("semantic expression changed: %s (%v)", convertedJSON, err)
	}
	config, err := CompileEngine(converted, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	pattern := config.Patterns[0].Slots[0]
	for i, expression := range want {
		if pattern.ExpressionAt(i) != kernelExpression(expression) {
			t.Fatalf("runtime step %d expression: %+v", i, pattern.ExpressionAt(i))
		}
	}
	if !converted.Patterns[0].Data[1].Tie || !converted.Patterns[0].Data[2].Tie || converted.Patterns[0].Data[3] != nil {
		t.Fatal("ties or rest changed during expression round-trip")
	}
}

func TestExpressionAbsentStorageRemainsNil(t *testing.T) {
	score, diagnostics := notation.Parse([]byte("track lead acid {} pattern take { c4 - } scene main { lead=take } song { main }"))
	if len(diagnostics) != 0 {
		t.Fatalf("source: %+v", diagnostics)
	}
	compiled, err := CompilePattern(score, score.Patterns[0], score.Tracks[0])
	if err != nil || compiled[0].Pattern.Expression != nil {
		t.Fatalf("expression-free source allocated expression storage: %v", err)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	config, err := CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range config.Patterns[0].Slots {
		if pattern.Expression != nil {
			t.Fatal("expression-free semantic project allocated expression storage")
		}
	}
}

func TestExpressionJSONRejectsInvalidShapeAndValues(t *testing.T) {
	for _, mutate := range []func(*Project){
		func(p *Project) { p.Patterns[0].Expression = p.Patterns[0].Expression[:1] },
		func(p *Project) { p.Patterns[0].Expression[0].PitchCents = 9601 },
		func(p *Project) { p.Patterns[0].Expression[0].Pressure = -0.1 },
		func(p *Project) { p.Patterns[0].Expression[0].Timbre = 1.1 },
		func(p *Project) { p.Patterns[0].Expression[0].VibratoDepthCents = -1 },
		func(p *Project) { p.Patterns[0].Expression[0].PitchCents = float32(math.NaN()) },
	} {
		p := expressionTestProject(t)
		mutate(p)
		if err := ValidateProject(p); err == nil {
			t.Fatal("invalid semantic expression was accepted")
		}
	}
	p := expressionTestProject(t)
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	expression := object["patterns"].([]any)[0].(map[string]any)["expression"].([]any)[0].(map[string]any)
	delete(expression, "pressure")
	encoded, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJSON(encoded); err == nil {
		t.Fatal("missing required expression pressure was accepted")
	}
}

func TestExpressionGraphDelayPitchBounds(t *testing.T) {
	const source = `instrument pluck { voice mono { out = comb(noise(), 1 / pitch, 0.8, 0.3) } }
track lead pluck {}
pattern take { c3 - bend: 0ct 0ct vibrato: 0ct 0ct }
scene main { lead=take }
song { main }
`
	for _, row := range []string{"bend: 0ct -9600ct", "bend: 0ct 9600ct", "vibrato: 0ct 9600ct"} {
		text := source
		if strings.HasPrefix(row, "bend:") {
			text = strings.Replace(text, "bend: 0ct 0ct", row, 1)
		} else {
			text = strings.Replace(text, "vibrato: 0ct 0ct", row, 1)
		}
		score, diagnostics := notation.Parse([]byte(text))
		if len(diagnostics) != 0 {
			t.Fatalf("row parse: %+v", diagnostics)
		}
		if _, diagnostics := Check(score); !hasErrors(diagnostics) {
			t.Fatalf("out-of-range tied graph period accepted for %s", row)
		}
	}
	score, diagnostics := notation.Parse([]byte(source))
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("valid graph pitch: %+v", diagnostics)
	}
	p.Patterns[0].Expression[1].PitchCents = -9600
	if err := ValidateProject(p); err == nil {
		t.Fatal("untrusted semantic expression bypassed graph delay bounds")
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJSON(encoded); err == nil {
		t.Fatal("untrusted JSON expression bypassed graph delay bounds")
	}
}

func TestExpressionChordRoundTripAndDelayBounds(t *testing.T) {
	const source = `instrument pluck { voice poly { out = comb(noise(), 1 / pitch, 0.8, 0.3) } }
track keys pluck {}
pattern take notes {
  [a4 c3] -
  bend: 0ct .
  vibrato: 0ct .
  pressure: . 0.7
  timbre: . 0.8
}

scene main { keys=take }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if len(diagnostics) != 0 {
		t.Fatalf("source: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("chord expression: %+v", diagnostics)
	}
	if !reflect.DeepEqual(p.Patterns[0].Data[0].Notes, []int{69, 48}) || !p.Patterns[0].Data[1].Tie || p.Patterns[0].Expression[1].Pressure != 0.7 {
		t.Fatalf("chord or expression lost: %+v", p.Patterns[0])
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, diagnostics := notation.Parse(text)
	restored, diagnostics := FromScore(roundtrip)
	if restored == nil {
		t.Fatalf("roundtrip: %+v", diagnostics)
	}
	again, err := CanonicalJSON(restored)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("chord expression changed during roundtrip: %v", err)
	}
	for _, row := range []string{"bend: 0ct -4800ct", "vibrato: 0ct 4800ct"} {
		original := strings.Split(row, ":")[0] + ": 0ct ."
		score, diagnostics := notation.Parse([]byte(strings.Replace(source, original, row, 1)))
		if len(diagnostics) != 0 {
			t.Fatalf("expression parse: %+v", diagnostics)
		}
		if _, diagnostics := Check(score); !hasErrors(diagnostics) {
			t.Fatalf("second held chord pitch bypassed delay bounds for %s", row)
		}
	}
	decoded.Patterns[0].Expression[1].PitchCents = -4800
	if err := ValidateProject(decoded); err == nil {
		t.Fatal("semantic second chord pitch bypassed expression delay bounds")
	}
}

func TestExpressionSamplerAssignmentRejectsUnsupportedControls(t *testing.T) {
	_, source, score := assetFixture(t)
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("valid sampler source: %+v", diagnostics)
	}
	for i := range p.Patterns {
		p.Patterns[i].Expression = make([]NoteExpression, p.Patterns[i].Steps)
		for j := range p.Patterns[i].Expression {
			p.Patterns[i].Expression[j].Timbre = 0.5
		}
	}
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
		t.Fatalf("sampler expression semantic validation: %v", err)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJSON(encoded); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
		t.Fatalf("sampler expression JSON validation: %v", err)
	}
	expressive := strings.Replace(string(source), "pattern hits { c3 . c3 . }", "pattern hits { c3 . c3 . bend: 0ct . . . }", 1)
	score, diagnostics = notation.ParseEdition([]byte(expressive), 2)
	if hasErrors(diagnostics) {
		t.Fatalf("sampler expression syntax: %+v", diagnostics)
	}
	if result, diagnostics := FromScore(score); result != nil || !hasErrors(diagnostics) {
		t.Fatalf("sampler expression source accepted: %+v", diagnostics)
	}
}

func TestExpressionDirectorStingerRoundTrip(t *testing.T) {
	source, err := os.ReadFile("../examples/game-director.cicada")
	if err != nil {
		t.Fatal(err)
	}
	source = bytes.Replace(source, []byte("pattern hit { 1'^ . . . }"), []byte("pattern hit { 1'^ - - . bend: 0ct 30ct . 0ct vibrato: 0ct 4ct . 0ct }"), 1)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("director expression source: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("director expression: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics = notation.Parse(text)
	if len(diagnostics) != 0 {
		t.Fatalf("director expression formatted source: %+v", diagnostics)
	}
	restored, diagnostics := FromScore(score)
	if restored == nil {
		t.Fatalf("director expression roundtrip: %+v", diagnostics)
	}
	again, err := CanonicalJSON(restored)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("director and stinger expression changed: %v", err)
	}
}
