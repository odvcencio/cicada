package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func rpc(t *testing.T, id int, method string, params any) []byte {
	t.Helper()
	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id > 0 {
		message["id"] = id
	}
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return Frame(body)
}

func TestServerDiagnosticsHoverHintsAndTokens(t *testing.T) {
	uri := "file:///music/chorus.cicada"
	source := "key e minor\ntrack bass acid {}\npattern hook acid steps=4 { 3?70 . 5 . }\nscene main { bass=hook }\nsong { main }\n"
	var input bytes.Buffer
	input.Write(rpc(t, 1, "initialize", map[string]any{}))
	input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "text": source}}))
	degree := strings.Index(source, "3?70")
	input.Write(rpc(t, 2, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": utf16Position([]byte(source), degree)}))
	input.Write(rpc(t, 3, "textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": uri}, "range": region{Start: position{}, End: position{Line: 99}}}))
	input.Write(rpc(t, 4, "textDocument/semanticTokens/full", map[string]any{"textDocument": map[string]any{"uri": uri}}))
	reference := strings.Index(source, "bass=hook") + len("bass=")
	input.Write(rpc(t, 5, "textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": utf16Position([]byte(source), reference)}))
	input.Write(rpc(t, 6, "textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": utf16Position([]byte(source), reference), "newName": "new-hook"}))
	input.Write(rpc(t, 0, "textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri}, "contentChanges": []any{map[string]any{"text": strings.Replace(source, "3?70", "8?70", 1)}}}))
	input.Write(rpc(t, 0, "textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}}))
	input.Write(rpc(t, 8, "cicada/unknown", map[string]any{}))
	input.Write(rpc(t, 7, "shutdown", map[string]any{}))
	input.Write(rpc(t, 0, "exit", map[string]any{}))
	var output bytes.Buffer
	if err := Serve(&input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	var messages []map[string]json.RawMessage
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
		messages = append(messages, message)
	}
	if len(messages) != 11 {
		t.Fatalf("got %d LSP messages", len(messages))
	}
	if !bytes.Contains(messages[0]["result"], []byte("semanticTokensProvider")) {
		t.Fatal("server capabilities missing")
	}
	if !bytes.Contains(messages[1]["params"], []byte(`"diagnostics":[]`)) {
		t.Fatalf("valid score diagnostics: %s", messages[1]["params"])
	}
	if !bytes.Contains(messages[2]["result"], []byte("degree 3 of E minor")) || !bytes.Contains(messages[2]["result"], []byte("G")) {
		t.Fatalf("degree hover: %s", messages[2]["result"])
	}
	if !bytes.Contains(messages[3]["result"], []byte("4 steps")) {
		t.Fatalf("inlay hint: %s", messages[3]["result"])
	}
	if !bytes.Contains(messages[3]["result"], []byte("bars 1–1")) {
		t.Fatalf("song position hint: %s", messages[3]["result"])
	}
	if !bytes.Contains(messages[4]["result"], []byte(`"data":[`)) {
		t.Fatalf("semantic tokens: %s", messages[4]["result"])
	}
	if !bytes.Contains(messages[5]["result"], []byte(`"line":2`)) {
		t.Fatalf("definition: %s", messages[5]["result"])
	}
	if bytes.Count(messages[6]["result"], []byte(`"newText":"new-hook"`)) != 2 {
		t.Fatalf("rename: %s", messages[6]["result"])
	}
	if !bytes.Contains(messages[7]["params"], []byte("CICADA-SYNTAX")) {
		t.Fatalf("invalid edit diagnostics: %s", messages[7]["params"])
	}
	if !bytes.Contains(messages[8]["params"], []byte(`"diagnostics":[]`)) {
		t.Fatalf("close diagnostics: %s", messages[8]["params"])
	}
	if !bytes.Contains(messages[9]["error"], []byte(`"code":-32601`)) {
		t.Fatalf("unknown method: %s", messages[9]["error"])
	}
}

func TestUTF16DocumentPositions(t *testing.T) {
	source := []byte("title \"🌙\"\npattern p { 1 }\n")
	offset := bytes.Index(source, []byte("pattern"))
	at := utf16Position(source, offset)
	if at.Line != 1 || at.Character != 0 || byteOffset(source, at) != offset {
		t.Fatalf("next line: %+v", at)
	}
	moon := bytes.Index(source, []byte("🌙"))
	if got := utf16Position(source, moon+len([]byte("🌙"))); got.Character != 9 {
		t.Fatalf("emoji UTF-16 width: %+v", got)
	}
}

func TestParameterHoverShowsDefaultAndTrackOverride(t *testing.T) {
	source := []byte("instrument tone { param cutoff = 720Hz voice mono { out = sine(cutoff) } }\ntrack lead tone { cutoff = 900Hz }\npattern p notes steps=1 { c }\nscene main { lead=p }\nsong { main }\n")
	reference := bytes.Index(source, []byte("sine(cutoff)")) + len("sine(")
	result, _ := json.Marshal(hover(source, utf16Position(source, reference)))
	if !bytes.Contains(result, []byte("720Hz")) || !bytes.Contains(result, []byte("900Hz")) {
		t.Fatalf("parameter hover: %s", result)
	}
	setting := bytes.Index(source, []byte("cutoff = 900Hz"))
	result, _ = json.Marshal(hover(source, utf16Position(source, setting)))
	if !bytes.Contains(result, []byte("Instrument default")) {
		t.Fatalf("track hover: %s", result)
	}
}
