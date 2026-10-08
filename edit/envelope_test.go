package edit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeCarriesAuthorSessionAndIntents(t *testing.T) {
	in := `{"version":1,"revision":"abc","author":"tester","session":"s1","intents":[{"kind":"probe","name":"x"}]}`
	var env Envelope
	if err := json.Unmarshal([]byte(in), &env); err != nil {
		t.Fatal(err)
	}
	if env.Author != "tester" || env.Session != "s1" || len(env.Intents) != 1 || env.Intents[0].Kind() != "probe" {
		t.Fatalf("%+v", env)
	}
	out, _ := json.Marshal(env)
	if string(out) != in {
		t.Fatalf("marshal: %s", out)
	}
	if err := json.Unmarshal([]byte(`{"version":2,"intents":[]}`), &env); err == nil || !strings.Contains(err.Error(), "envelope version 2") {
		t.Fatalf("version: %v", err)
	}
}
