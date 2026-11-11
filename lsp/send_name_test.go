package lsp

import (
	"bytes"
	"testing"
)

// Completion offers a send by the return's declared name, as the score takes
// it (a.send.hall), beside the return keywords, and only for send returns.
func TestPathCompletionOffersSendNames(t *testing.T) {
	source := []byte("fx hall reverb {}\nfx echo tape {}\nfx grit drive {}\ntrack a acid { send hall = 0.2 }\nscene main { a.send.\n}\n")
	items := parameterPathCompletion(source, utf16Position(source, bytes.Index(source, []byte("a.send."))+len("a.send.")))
	labels := map[string]bool{}
	for _, item := range items {
		labels[item["label"].(string)] = true
	}
	for _, want := range []string{"delay", "reverb", "hall", "echo"} {
		if !labels[want] {
			t.Errorf("completion lacks send.%s: %v", want, labels)
		}
	}
	if labels["grit"] {
		t.Error("completion offers a send to a drive")
	}
}
