package agentsuite

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// mcpClient drives cicada mcp over its standard input and output, one
// JSON-RPC message per line, and fails on any output line that is not one.
type mcpClient struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  *bufio.Scanner
	nextID int
}

func startMCP(t *testing.T, bin, root string) *mcpClient {
	t.Helper()
	cmd := exec.Command(bin, "mcp", "--root", root)
	cmd.Env = append(os.Environ(), "CICADA_LIBRARY="+t.TempDir())
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 64<<10), 8<<20)
	client := &mcpClient{t: t, cmd: cmd, stdin: stdin, lines: lines}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return client
}

var modernMeta = map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "agentsuite", "version": "1"}}

// send writes one message; with an id it reads the reply.
func (c *mcpClient) send(method string, params map[string]any, notification bool) map[string]any {
	c.t.Helper()
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		message["params"] = params
	}
	if !notification {
		c.nextID++
		message["id"] = c.nextID
	}
	data, _ := json.Marshal(message)
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		c.t.Fatal(err)
	}
	if notification {
		return nil
	}
	if !c.lines.Scan() {
		c.t.Fatalf("%s: the server closed its output: %v", method, c.lines.Err())
	}
	var reply map[string]any
	if err := json.Unmarshal(c.lines.Bytes(), &reply); err != nil || reply["jsonrpc"] != "2.0" || reply["id"] != float64(c.nextID) {
		c.t.Fatalf("%s: not the JSON-RPC reply: %q", method, c.lines.Text())
	}
	return reply
}

// modern sends a request with the per-request _meta of 2026-07-28.
func (c *mcpClient) modern(method string, params map[string]any) map[string]any {
	c.t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = modernMeta
	return c.send(method, params, false)
}

// call runs a tool and returns its structured document and isError, after
// checking that the text block holds the same document.
func (c *mcpClient) call(reply map[string]any) (map[string]any, bool) {
	c.t.Helper()
	result, ok := reply["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("tools/call failed: %v", reply)
	}
	document, _ := result["structuredContent"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text), &fromText); err != nil || mustJSONString(fromText) != mustJSONString(document) {
		c.t.Fatalf("the text block is not the structured document: %q", text)
	}
	isError, _ := result["isError"].(bool)
	return document, isError
}

func mustJSONString(v any) string { return string(mustJSON(v)) }

// close ends the input and requires the server to exit cleanly within 2 s.
func (c *mcpClient) close() {
	c.t.Helper()
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			c.t.Fatalf("cicada mcp exited with %v after its input closed", err)
		}
	case <-time.After(2 * time.Second):
		c.t.Fatal("cicada mcp did not exit within 2 s of its input closing")
	}
	if c.lines.Scan() {
		c.t.Fatalf("cicada mcp wrote after the last reply: %q", c.lines.Text())
	}
}

// driveMCPServer runs cicada mcp against a new project through the protocol,
// in both eras, and requires each tool to return the command line's own
// documents.
func driveMCPServer(t *testing.T, bin string) {
	t.Helper()
	dir := t.TempDir()
	if _, code := cicada(t, bin, dir, "new", "demo"); code != 0 {
		t.Fatal("cicada new failed")
	}
	root := filepath.Join(dir, "demo")

	started := time.Now()
	c := startMCP(t, bin, root)
	discover := c.modern("server/discover", nil)["result"].(map[string]any)
	startup := time.Since(started)
	if discover["resultType"] != "complete" || discover["supportedVersions"].([]any)[0] != "2026-07-28" || discover["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("server/discover: %v", discover)
	}
	tools := c.modern("tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tool := range tools {
		tool := tool.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["inputSchema"] == nil || tool["outputSchema"] == nil || tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Fatalf("tool %v lacks a schema or the read-only hint", tool["name"])
		}
	}
	if mustJSONString(names) != `["song","check","intents","explain","library"]` {
		t.Fatalf("tools/list: %v", names)
	}

	// check returns check --json, as the command prints it in the root.
	called := time.Now()
	document, isError := c.call(c.modern("tools/call", map[string]any{"name": "check", "arguments": map[string]any{"score": "main.cicada"}}))
	roundTrip := time.Since(called)
	stdout, code := cicada(t, bin, root, "check", "--json", "main.cicada")
	var cli map[string]any
	if err := json.Unmarshal(stdout, &cli); err != nil || code != 0 || isError || mustJSONString(document) != mustJSONString(cli) {
		t.Fatalf("check through the server differs from check --json:\n%v\n%s", document, stdout)
	}
	for _, tc := range []struct {
		tool      string
		arguments map[string]any
		schema    string
	}{
		{"song", map[string]any{}, "cicada.song.v1"},
		{"song", map[string]any{"score": "main.cicada", "include": []string{"symbols"}}, "cicada.song.v1"},
		{"intents", map[string]any{}, "cicada.intents_index.v1"},
		{"intents", map[string]any{"kinds": []string{"setparam"}}, "cicada.intents.v1"},
		{"explain", map[string]any{"diagnostic": "CICADA-PARAM"}, "cicada.diagnostics.v1"},
		{"explain", map[string]any{"construct": "pattern"}, "cicada.explain.v1"},
		{"library", map[string]any{}, "cicada.library.v1"},
		{"library", map[string]any{"path": "std/drums"}, "cicada.library.v1"},
	} {
		document, isError := c.call(c.modern("tools/call", map[string]any{"name": tc.tool, "arguments": tc.arguments}))
		if isError || document["schema"] != tc.schema || document["error"] != nil {
			t.Fatalf("%s %v: isError %v, %v", tc.tool, tc.arguments, isError, document)
		}
	}
	// A refusal is a result with isError and the tool's own format; an
	// unknown tool and an unsupported version are protocol errors.
	document, isError = c.call(c.modern("tools/call", map[string]any{"name": "check", "arguments": map[string]any{"score": "../outside.cicada"}}))
	if failure, _ := document["error"].(map[string]any); !isError || document["schema"] != "cicada.check.v1" || failure["code"] != "path_outside_root" {
		t.Fatalf("a path out of the root: %v %v", isError, document)
	}
	if failure, _ := c.modern("tools/call", map[string]any{"name": "render", "arguments": map[string]any{}})["error"].(map[string]any); failure["code"] != float64(-32602) {
		t.Fatalf("an unknown tool: %v", failure)
	}
	old := c.send("tools/list", map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "1900-01-01", "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}, false)
	if failure, _ := old["error"].(map[string]any); failure["code"] != float64(-32022) {
		t.Fatalf("an unsupported version: %v", old)
	}
	c.close()

	// The legacy era: initialize, then the same tools.
	legacy := startMCP(t, bin, root)
	initialized := legacy.send("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "agentsuite", "version": "1"}}, false)["result"].(map[string]any)
	if initialized["protocolVersion"] != "2025-06-18" || initialized["serverInfo"].(map[string]any)["name"] != "cicada" {
		t.Fatalf("initialize: %v", initialized)
	}
	legacy.send("notifications/initialized", nil, true)
	if list := legacy.send("tools/list", nil, false)["result"].(map[string]any); len(list["tools"].([]any)) != 5 {
		t.Fatalf("legacy tools/list: %v", list)
	}
	if document, isError := legacy.call(legacy.send("tools/call", map[string]any{"name": "check", "arguments": map[string]any{"score": "main.cicada"}}, false)); isError || !bytes.Equal(mustJSON(document), mustJSON(cli)) {
		t.Fatalf("legacy check: %v %v", isError, document)
	}
	legacy.close()
	t.Logf("cicada mcp: started and answered server/discover in %s; one check call round trip %s", startup.Round(time.Millisecond), roundTrip.Round(time.Millisecond))
}
