package agentsuite

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
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
	tmp    string // the server's TMPDIR, which holds its stand-in user library
}

func startMCP(t *testing.T, bin, root string) *mcpClient {
	t.Helper()
	cmd := exec.Command(bin, "mcp", "--root", root)
	tmp := t.TempDir()
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
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
	client := &mcpClient{t: t, cmd: cmd, stdin: stdin, lines: lines, tmp: tmp}
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
	c.requireNoStandIn()
}

// requireNoStandIn fails while the server's empty stand-in for the user
// library is still in its TMPDIR.
func (c *mcpClient) requireNoStandIn() {
	c.t.Helper()
	if entries, err := os.ReadDir(c.tmp); err != nil || len(entries) != 0 {
		c.t.Fatalf("cicada mcp left %v in its TMPDIR (%v)", entries, err)
	}
}

// driveMCPServer runs cicada mcp against a new project through the protocol,
// in both eras, and requires each tool to return the command line's own
// documents.
func driveMCPServer(t *testing.T, bin string) {
	t.Helper()
	// The server reads no user library; neither may the commands it is
	// compared with.
	previous, had := os.LookupEnv("CICADA_LIBRARY")
	if err := os.Setenv("CICADA_LIBRARY", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if had {
			_ = os.Setenv("CICADA_LIBRARY", previous)
		} else {
			_ = os.Unsetenv("CICADA_LIBRARY")
		}
	}()
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
	if mustJSONString(names) != `["song","check","intents","explain","library","params"]` {
		t.Fatalf("tools/list: %v", names)
	}

	// Every tool returns, by value, the document its command prints in the
	// root; check is also timed.
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
		command   []string
	}{
		{"song", map[string]any{}, []string{"song", "--json"}},
		{"song", map[string]any{"score": "main.cicada", "include": []string{"symbols"}}, []string{"song", "--json", "--include", "symbols", "main.cicada"}},
		{"check", map[string]any{"score": "main.cicada", "profile": "browser"}, []string{"check", "--json", "--profile", "browser", "main.cicada"}},
		{"intents", map[string]any{}, []string{"intents", "--json", "--index"}},
		{"intents", map[string]any{"kinds": []string{"setparam"}}, []string{"intents", "--json", "setparam"}},
		{"explain", map[string]any{"diagnostic": "CICADA-PARAM"}, []string{"diagnostics", "--json", "CICADA-PARAM"}},
		{"explain", map[string]any{"construct": "pattern"}, []string{"explain", "pattern", "--json"}},
		{"library", map[string]any{}, []string{"lib", "list", "--json"}},
		{"library", map[string]any{"path": "std/drums"}, []string{"lib", "show", "std/drums", "--json"}},
		{"params", map[string]any{}, []string{"params", "--json", "--index"}},
		{"params", map[string]any{"voice": "fm_bass"}, []string{"params", "--json", "--voice", "fm_bass"}},
		{"params", map[string]any{"scope": "effect", "control": "delay"}, []string{"params", "--json", "--scope", "effect", "delay"}},
	} {
		document, isError := c.call(c.modern("tools/call", map[string]any{"name": tc.tool, "arguments": tc.arguments}))
		stdout, code := cicada(t, bin, root, tc.command...)
		var want any
		if err := json.Unmarshal(stdout, &want); err != nil || code != 0 || isError || mustJSONString(document) != mustJSONString(want) {
			t.Fatalf("%s %v differs from cicada %v (isError %v):\n%s\n%s", tc.tool, tc.arguments, tc.command, isError, mustJSONString(document), stdout)
		}
	}
	document, isError = c.call(c.modern("tools/call", map[string]any{"name": "params", "arguments": map[string]any{"voice": "nosuch"}}))
	if !isError || document["schema"] != "cicada.params.v1" || document["error"].(map[string]any)["code"] != "no_match" {
		t.Fatalf("params with an unknown voice: %v %v", isError, document)
	}

	// The guide resources are what cicada agents and cicada agents reference
	// print, so an agent with only the server can start.
	resources := c.modern("resources/list", nil)["result"].(map[string]any)["resources"].([]any)
	if len(resources) != 2 {
		t.Fatalf("resources/list: %v", resources)
	}
	for i, page := range [][]string{{"agents"}, {"agents", "reference"}} {
		uri := resources[i].(map[string]any)["uri"].(string)
		read := c.modern("resources/read", map[string]any{"uri": uri})["result"].(map[string]any)
		text := read["contents"].([]any)[0].(map[string]any)["text"].(string)
		stdout, code := cicada(t, bin, root, page...)
		if code != 0 || text != string(stdout) || resources[i].(map[string]any)["size"] != float64(len(stdout)) {
			t.Fatalf("resource %s differs from cicada %v", uri, page)
		}
	}
	if failure, _ := c.modern("resources/read", map[string]any{"uri": "cicada://guide/nosuch"})["error"].(map[string]any); failure["code"] != float64(-32602) {
		t.Fatalf("an unknown resource: %v", failure)
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
	if list := legacy.send("tools/list", nil, false)["result"].(map[string]any); len(list["tools"].([]any)) != 6 {
		t.Fatalf("legacy tools/list: %v", list)
	}
	if document, isError := legacy.call(legacy.send("tools/call", map[string]any{"name": "check", "arguments": map[string]any{"score": "main.cicada"}}, false)); isError || !bytes.Equal(mustJSON(document), mustJSON(cli)) {
		t.Fatalf("legacy check: %v %v", isError, document)
	}
	if read := legacy.send("resources/read", map[string]any{"uri": "cicada://guide/quickstart"}, false); read["result"] == nil {
		t.Fatalf("legacy resources/read: %v", read)
	}
	legacy.close()

	// Stopped by a signal instead of closing its input, the server still
	// removes its stand-in folder.
	stopped := startMCP(t, bin, root)
	stopped.modern("server/discover", nil)
	if entries, _ := os.ReadDir(stopped.tmp); len(entries) != 1 {
		t.Fatalf("the running server's TMPDIR holds %v, not its one stand-in folder", entries)
	}
	if err := stopped.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = stopped.cmd.Wait()
	stopped.requireNoStandIn()
	t.Logf("cicada mcp: started and answered server/discover in %s; one check call round trip %s", startup.Round(time.Millisecond), roundTrip.Round(time.Millisecond))
}
