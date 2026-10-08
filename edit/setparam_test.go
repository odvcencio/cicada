package edit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSetParamWritesInsideTrackAndResetsCleanly(t *testing.T) {
	fixture, param := setParamFixture(t)
	env := Envelope{Version: 1, Intents: []Intent{&SetParam{Entity: EntityID("param:keys." + param), Value: json.RawMessage(`"0.5"`)}}}
	result, err := Apply(fixture, env, Options{Compiler: parseCompiler{}})
	if err != nil {
		t.Fatal(err)
	}
	window := changedWindow(fixture, result.Source)
	if !strings.HasPrefix(string(window), " "+param+" = ") || result.Label != "Set keys."+param {
		t.Fatalf("window %q label %q", window, result.Label)
	}
	reset, err := Apply(result.Source, Envelope{Version: 1, Intents: []Intent{&SetParam{Entity: EntityID("param:keys." + param)}}}, Options{Compiler: parseCompiler{}})
	// Reset removes only the override, so the braces keep the space the set added.
	wantReset := bytes.Replace(fixture, []byte("track keys my-pad {}"), []byte("track keys my-pad {  }"), 1)
	if err != nil || !bytes.Equal(reset.Source, wantReset) || reset.Label != "Use default keys."+param {
		t.Fatalf("reset: %v %q", err, reset.Label)
	}
	_, err = Apply(fixture, Envelope{Version: 1, Intents: []Intent{&SetParam{Entity: "param:keys.nope", Value: json.RawMessage(`1`)}}}, Options{Compiler: parseCompiler{}})
	if err == nil || err.Error() != `instrument my-pad does not declare parameter "nope"` {
		t.Fatalf("error text parity: %v", err)
	}
}
