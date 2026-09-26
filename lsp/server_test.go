package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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

func TestCodeActionSharesValidatedCicadaFixAndDocumentVersion(t *testing.T) {
	uri := fileURI(filepath.Join(t.TempDir(), "legacy.cicada"))
	source := "cicada 1\ntitle \"🌙\"\ntrack bass acid {}\npattern p acid steps=1 { 1%70 }\nscene main { bass=p }\nsong { main }\n"
	var input bytes.Buffer
	input.Write(rpc(t, 1, "initialize", map[string]any{"capabilities": map[string]any{"workspace": map[string]any{"workspaceEdit": map[string]any{"documentChanges": true, "resourceOperations": []string{"create"}}}}}))
	input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 7, "text": source}}))
	request := map[string]any{"textDocument": map[string]any{"uri": uri}, "range": region{}, "context": map[string]any{"diagnostics": []any{}}}
	input.Write(rpc(t, 2, "textDocument/codeAction", request))
	input.Write(rpc(t, 0, "textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 8}, "contentChanges": []any{map[string]any{"text": "invalid score"}}}))
	input.Write(rpc(t, 3, "textDocument/codeAction", request))
	input.Write(rpc(t, 0, "exit", map[string]any{}))
	var output bytes.Buffer
	if err := Serve(&input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	var frames []map[string]json.RawMessage
	for {
		body, err := readFrame(reader)
		if err == io.EOF {
			break
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
	if len(frames) != 5 || !bytes.Contains(frames[0]["result"], []byte(`"codeActionProvider":true`)) {
		t.Fatalf("capability or frame count: %d", len(frames))
	}
	var actions []struct {
		Title string `json:"title"`
		Kind  string `json:"kind"`
		Edit  struct {
			DocumentChanges []struct {
				Kind         string `json:"kind"`
				URI          string `json:"uri"`
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
				Edits []struct {
					Range   region `json:"range"`
					NewText string `json:"newText"`
				} `json:"edits"`
			} `json:"documentChanges"`
		} `json:"edit"`
	}
	if err := json.Unmarshal(frames[2]["result"], &actions); err != nil || len(actions) != 1 {
		t.Fatalf("code action: %s, %v", frames[2]["result"], err)
	}
	if len(actions[0].Edit.DocumentChanges) != 3 || actions[0].Edit.DocumentChanges[0].Kind != "create" || !strings.HasSuffix(actions[0].Edit.DocumentChanges[0].URI, "/cicada.mod") {
		t.Fatalf("manifest create missing: %+v", actions[0].Edit.DocumentChanges)
	}
	if manifest := actions[0].Edit.DocumentChanges[1].Edits[0].NewText; manifest != "project legacy\ncicada 1\n" {
		t.Fatalf("manifest content: %q", manifest)
	}
	change := actions[0].Edit.DocumentChanges[2]
	if actions[0].Kind != "quickfix" || change.TextDocument.URI != uri || change.TextDocument.Version != 7 || change.Edits[0].Range.End != utf16Position([]byte(source), len(source)) {
		t.Fatalf("versioned action: %+v", actions[0])
	}
	fixed := change.Edits[0].NewText
	if strings.Contains(fixed, "cicada 1") || strings.Contains(fixed, "1%70") || !strings.Contains(fixed, "1?70") || !strings.Contains(fixed, "🌙") {
		t.Fatalf("fix changed unexpected source: %s", fixed)
	}
	if string(frames[4]["result"]) != "[]" {
		t.Fatalf("invalid source offered fix: %s", frames[4]["result"])
	}
}

func TestCodeActionKeepsEditionWhenManifestCreationIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	uri := fileURI(filepath.Join(dir, "score.cicada"))
	source := "cicada 1\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n"
	params := map[string]any{"textDocument": map[string]any{"uri": uri}, "range": region{}, "context": map[string]any{"diagnostics": []any{}}}
	var input bytes.Buffer
	input.Write(rpc(t, 1, "initialize", map[string]any{"capabilities": map[string]any{"workspace": map[string]any{"workspaceEdit": map[string]any{"documentChanges": true}}}}))
	input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "text": source}}))
	input.Write(rpc(t, 2, "textDocument/codeAction", params))
	manifest := filepath.Join(dir, "cicada.mod")
	// Serve processes the buffered requests after setup, so test the existing
	// manifest case in a separate run below.
	var output bytes.Buffer
	if err := Serve(&input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	for i := 0; i < 2; i++ {
		if _, err := readFrame(reader); err != nil {
			t.Fatal(err)
		}
	}
	body, err := readFrame(reader)
	if err != nil || !bytes.Contains(body, []byte(`"result":[]`)) {
		t.Fatalf("header removal offered without manifest support: %s, %v", body, err)
	}
	if err := os.WriteFile(manifest, []byte("project score\ncicada 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input.Reset()
	output.Reset()
	input.Write(rpc(t, 1, "initialize", map[string]any{"capabilities": map[string]any{"workspace": map[string]any{"workspaceEdit": map[string]any{"documentChanges": true}}}}))
	input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "text": source}}))
	input.Write(rpc(t, 2, "textDocument/codeAction", params))
	if err := Serve(&input, &output); err != nil {
		t.Fatal(err)
	}
	reader = bufio.NewReader(&output)
	for i := 0; i < 2; i++ {
		if _, err := readFrame(reader); err != nil {
			t.Fatal(err)
		}
	}
	body, err = readFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result []struct {
			Edit struct {
				DocumentChanges []json.RawMessage `json:"documentChanges"`
			} `json:"edit"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil || len(response.Result) != 1 || len(response.Result[0].Edit.DocumentChanges) != 1 || bytes.Contains(body, []byte(`"kind":"create"`)) {
		t.Fatalf("existing manifest action: %s, %v", body, err)
	}
}

func TestCodeActionHonorsRequestedKinds(t *testing.T) {
	uri := fileURI(filepath.Join(t.TempDir(), "legacy.cicada"))
	source := []byte("cicada 1\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n")
	var output bytes.Buffer
	s := &server{out: &output, documents: map[string][]byte{uri: source}, versions: map[string]*int{}, canEditDocuments: true, canCreateFiles: true}
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}, "context": map[string]any{"only": []string{"source"}}})
	if err := s.handle(request{ID: json.RawMessage("1"), Method: "textDocument/codeAction", Params: params}); err != nil {
		t.Fatal(err)
	}
	body, err := readFrame(bufio.NewReader(&output))
	if err != nil || !bytes.Contains(body, []byte(`"result":[]`)) {
		t.Fatalf("unexpected action kind: %s, %v", body, err)
	}
}

func TestWindowsUNCScoreURIAndManifestRoundTrip(t *testing.T) {
	path, ok := scorePathFromURIForOS("file://server/share/Music%20Room/beat.cicada", true)
	if !ok || path != `\\server\share\Music Room\beat.cicada` {
		t.Fatalf("UNC score path: %q, %t", path, ok)
	}
	manifest := fileURIForOS(`\\server\share\Music Room\cicada.mod`, true)
	if manifest != "file://server/share/Music%20Room/cicada.mod" {
		t.Fatalf("UNC manifest URI: %s", manifest)
	}
	if _, ok := scorePathFromURIForOS("file://server/share/beat.cicada", false); ok {
		t.Fatal("Unix accepted a remote file authority")
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
