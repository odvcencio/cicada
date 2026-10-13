package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// The song-position hint after each block is the elapsed time, summed across
// the scene tempo steps.
func TestSongHintsSumSceneTempoSteps(t *testing.T) {
	uri := "file:///music/steps.cicada"
	source := "tempo 120\ntrack bass acid {}\npattern hook acid steps=4 { 3 . 5 . }\nscene a { bass=hook }\nscene b { bass=hook tempo = 60 }\nsong { a*2 b*2 }\n"
	var input bytes.Buffer
	input.Write(rpc(t, 1, "initialize", map[string]any{}))
	input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "text": source}}))
	input.Write(rpc(t, 2, "textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": uri}, "range": region{Start: position{}, End: position{Line: 99}}}))
	input.Write(rpc(t, 3, "shutdown", map[string]any{}))
	input.Write(rpc(t, 0, "exit", map[string]any{}))
	var output bytes.Buffer
	if err := Serve(&input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	var hints string
	for {
		body, err := readFrame(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		if string(message["id"]) == "2" {
			hints = string(message["result"])
		}
	}
	// Two bars at 120 BPM end at 0:04.0; two more at 60 BPM take 8 s and end at 0:12.0.
	for _, want := range []string{"bars 1–2, 0:04.0", "bars 3–4, 0:12.0"} {
		if !strings.Contains(hints, want) {
			t.Fatalf("hints lack %q: %s", want, hints)
		}
	}
}
