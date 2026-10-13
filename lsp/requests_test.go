package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func serveFrames(t *testing.T, input *bytes.Buffer) []map[string]json.RawMessage {
	t.Helper()
	var output bytes.Buffer
	if err := Serve(input, &output); err != nil {
		t.Fatalf("Serve ended the session: %v", err)
	}
	reader := bufio.NewReader(&output)
	var frames []map[string]json.RawMessage
	for {
		body, err := readFrame(reader)
		if err == io.EOF {
			return frames
		}
		if err != nil {
			t.Fatal(err)
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(body, &frame); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
}

func replyFor(frames []map[string]json.RawMessage, id string) map[string]json.RawMessage {
	for _, frame := range frames {
		if string(frame["id"]) == id {
			return frame
		}
	}
	return nil
}

// LSP diagnostic codes may be integers. A numeric code is some other tool's:
// it yields a normal reply and the session goes on.
func TestCodeActionAcceptsNumericAndStringDiagnosticCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.cicada")
	uri := fileURI(path)
	source := "cicada 2\ntrack bass acid {}\npattern hits { c3 . c4 . }\narrange {\n  place a bass hits { at = 100ticks length = 1bar }\n}\n"
	diagnostics := []any{
		map[string]any{"range": region{Start: position{Line: 4}}, "code": 42, "message": "step pattern placement must start on a step"},
		map[string]any{"range": region{Start: position{Line: 4}}, "message": "no code"},
		map[string]any{"range": region{Start: position{Line: 4}}, "code": nil, "message": "null code"},
	}
	var input bytes.Buffer
	input.Write(rpc(t, 1, "initialize", map[string]any{"capabilities": map[string]any{"workspace": map[string]any{"workspaceEdit": map[string]any{"documentChanges": true}}}}))
	input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "text": source}}))
	input.Write(rpc(t, 2, "textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": uri}, "range": region{}, "context": map[string]any{"diagnostics": diagnostics}}))
	input.Write(rpc(t, 3, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position{}}))
	frames := serveFrames(t, &input)
	reply := replyFor(frames, "2")
	if reply == nil || reply["error"] != nil || strings.Contains(string(reply["result"]), "Snap placement") {
		t.Fatalf("numeric code reply: %s", reply)
	}
	if replyFor(frames, "3") == nil {
		t.Fatal("request after the numeric code got no reply")
	}
}

// A request whose parameters do not decode gets InvalidParams for that
// request only; notifications are dropped; the session continues.
func TestMalformedRequestsAreRequestScoped(t *testing.T) {
	uri := fileURI(filepath.Join(t.TempDir(), "main.cicada"))
	var input bytes.Buffer
	input.Write(rpc(t, 1, "initialize", map[string]any{"capabilities": map[string]any{}}))
	input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "text": "title \"x\"\n"}}))
	for i, method := range []string{"textDocument/hover", "textDocument/definition", "textDocument/rename", "textDocument/completion", "textDocument/inlayHint", "textDocument/codeAction", "textDocument/semanticTokens/full"} {
		input.Write(Frame([]byte(`{"jsonrpc":"2.0","id":` + strconv.Itoa(10+i) + `,"method":"` + method + `","params":{"textDocument":7,"position":"x","range":[],"context":5}}`)))
	}
	// Notifications with bad parameters get no reply.
	input.Write(Frame([]byte(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":[],"contentChanges":3}}`)))
	input.Write(Frame([]byte(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":"nope"}`)))
	// A frame that is not JSON gets a parse error.
	input.Write(Frame([]byte(`{not json`)))
	input.Write(rpc(t, 9, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": position{}}))
	frames := serveFrames(t, &input)
	errors := 0
	for _, frame := range frames {
		if frame["error"] == nil {
			continue
		}
		errors++
		code := string(frame["error"])
		if string(frame["id"]) == "null" != strings.Contains(code, "-32700") || string(frame["id"]) != "null" && !strings.Contains(code, "-32602") {
			t.Fatalf("unexpected error: %s %s", frame["id"], code)
		}
	}
	// Seven invalid requests and one parse error; nothing for the two notifications.
	if errors != 8 {
		t.Fatalf("%d error replies, want 8: %v", errors, frames)
	}
	if reply := replyFor(frames, "9"); reply == nil || reply["error"] != nil {
		t.Fatalf("the request after the bad ones failed: %v", reply)
	}
}
