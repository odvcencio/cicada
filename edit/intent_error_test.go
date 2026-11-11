package edit

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestEnvelopeDecodeErrorsNameTheIntentAndField(t *testing.T) {
	for _, tc := range []struct {
		body, kind, field, location string
		index                       int
	}{
		{`[{"kind":"togglestep","entity":"step:a/0"},{"kind":"setpitch","entity":"step:a/1","pitch":"high"}]`, "setpitch", "pitch", "intents[1].pitch (setpitch)", 1},
		{`[{"kind":"togglestep","entity":"step:a/0","colour":1}]`, "togglestep", "colour", "intents[0].colour (togglestep)", 0},
		{`[{"kind":"togglestep","entity":"step:a/0"},{"kind":"nosuchkind"}]`, "", "kind", "intents[1].kind", 1},
		{`[{"entity":"step:a/0"}]`, "", "kind", "intents[0].kind", 0},
	} {
		var env Envelope
		err := json.Unmarshal([]byte(`{"version":1,"intents":`+tc.body+`}`), &env)
		var located *IntentError
		if !errors.As(err, &located) || located.Index != tc.index || located.Kind != tc.kind || located.Field != tc.field || located.Location() != tc.location {
			t.Errorf("%s: got %#v (%v)", tc.body, located, err)
			continue
		}
		if located.Error() != located.Err.Error() {
			t.Errorf("%s: the message changed: %q", tc.body, located.Error())
		}
	}
}

func TestApplyErrorsNameTheIntentAndKeepTheMessage(t *testing.T) {
	source := []byte("track bass acid {}\npattern p acid { 1 . 5 . }\nscene main { bass=p }\nsong { main*2 }\n")
	for _, tc := range []struct {
		second       Intent
		field, cause string
	}{
		{&ToggleStep{Entity: "pattern:p"}, "entity", `grid edit needs a step entity, got "pattern:p"`},
		{&ToggleStep{Entity: "step:nosuch/0"}, "entity", `unknown pattern "nosuch"`},
		{&ToggleModifier{Entity: "step:p/0", Modifier: "wobble"}, "", "a note pattern, nonnegative step, and accent or slide are required"},
	} {
		env := Envelope{Version: 1, Intents: []Intent{&ToggleStep{Entity: "step:p/1"}, tc.second}}
		_, err := Apply(source, env, Options{Compiler: parseCompiler{}})
		var located *IntentError
		if !errors.As(err, &located) || located.Index != 1 || located.Kind != tc.second.Kind() || located.Field != tc.field || err.Error() != tc.cause {
			t.Errorf("%T: got %#v (%v)", tc.second, located, err)
		}
	}
}

func TestApplyLabelsEachIntent(t *testing.T) {
	source := []byte("track bass acid {}\npattern p acid { 1 . 5 . }\nscene main { bass=p }\nsong { main*2 }\n")
	env := Envelope{Version: 1, Intents: []Intent{&ToggleStep{Entity: "step:p/1"}, &ToggleModifier{Entity: "step:p/0", Modifier: "accent"}}}
	result, err := Apply(source, env, Options{Compiler: parseCompiler{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Labels) != 2 || result.Labels[0] != "Grid · p step 2 toggled" || result.Labels[1] != "Grid · p step 1 accent changed" || result.Label != result.Labels[1] {
		t.Fatalf("labels %q, label %q", result.Labels, result.Label)
	}
}
