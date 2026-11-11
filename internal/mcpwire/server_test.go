package mcpwire

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

func testServer() *Server {
	return &Server{
		Name: "test", Version: "1", Instructions: "use the echo tool",
		Tools: []Tool{{Name: "echo", Description: "echoes", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Call: func(name string, arguments json.RawMessage) (Result, error) {
			if name != "echo" {
				return Result{}, &ToolError{Code: CodeInvalidParams, Message: "unknown tool: " + name}
			}
			return Result{Document: json.RawMessage(`{"schema":"test.v1","got":` + string(arguments) + `}`), IsError: strings.Contains(string(arguments), "fail")}, nil
		},
	}
}

const modernMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

// exchange sends the lines to a server and returns one decoded message per
// line it wrote.
func exchange(t *testing.T, lines ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := testServer().Serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var messages []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(out.String()))
	for scanner.Scan() {
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatalf("not a JSON line: %q", scanner.Text())
		}
		messages = append(messages, message)
	}
	return messages
}

func errorCode(message map[string]any) float64 {
	failure, _ := message["error"].(map[string]any)
	code, _ := failure["code"].(float64)
	return code
}

func TestModernRequests(t *testing.T) {
	replies := exchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"a":1},`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"fail":true},`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nosuch",`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":6,"method":"nosuch/method","params":{`+modernMeta+`}}`,
	)
	if len(replies) != 6 {
		t.Fatalf("%d replies to 6 requests and a notification: %v", len(replies), replies)
	}
	discover := replies[0]["result"].(map[string]any)
	if discover["resultType"] != "complete" || discover["supportedVersions"].([]any)[0] != ModernVersion || discover["instructions"] != "use the echo tool" || discover["_meta"].(map[string]any)[metaServerInfo] == nil {
		t.Fatalf("discover: %v", discover)
	}
	list := replies[1]["result"].(map[string]any)
	if list["tools"].([]any)[0].(map[string]any)["name"] != "echo" || list["cacheScope"] != "public" || list["ttlMs"] == nil {
		t.Fatalf("tools/list: %v", list)
	}
	call := replies[2]["result"].(map[string]any)
	structured := call["structuredContent"].(map[string]any)
	text := call["content"].([]any)[0].(map[string]any)["text"].(string)
	if call["isError"] != false || structured["schema"] != "test.v1" || !strings.Contains(text, `"got":{"a":1}`) || call["resultType"] != "complete" {
		t.Fatalf("tools/call: %v", call)
	}
	if replies[3]["result"].(map[string]any)["isError"] != true {
		t.Fatalf("a refusing tool: %v", replies[3])
	}
	if errorCode(replies[4]) != CodeInvalidParams || errorCode(replies[5]) != CodeMethodNotFound {
		t.Fatalf("unknown tool and method: %v %v", replies[4], replies[5])
	}
}

func TestVersionAndMetaRules(t *testing.T) {
	replies := exchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`not json`,
	)
	if len(replies) != 4 {
		t.Fatalf("%d replies: %v", len(replies), replies)
	}
	unsupported := replies[0]["error"].(map[string]any)
	data := unsupported["data"].(map[string]any)
	if errorCode(replies[0]) != CodeUnsupportedVersion || data["requested"] != "1900-01-01" || data["supported"].([]any)[0] != ModernVersion {
		t.Fatalf("unsupported version: %v", replies[0])
	}
	if errorCode(replies[1]) != CodeInvalidParams || errorCode(replies[2]) != CodeInvalidParams || errorCode(replies[3]) != CodeParse {
		t.Fatalf("missing capabilities, missing _meta, parse error: %v", replies[1:])
	}
}

func TestLegacyHandshake(t *testing.T) {
	replies := exchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"old","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
	)
	if len(replies) != 4 {
		t.Fatalf("%d replies: %v", len(replies), replies)
	}
	initialized := replies[0]["result"].(map[string]any)
	if initialized["protocolVersion"] != "2025-06-18" || initialized["serverInfo"].(map[string]any)["name"] != "test" {
		t.Fatalf("initialize: %v", initialized)
	}
	list := replies[1]["result"].(map[string]any)
	if _, modern := list["resultType"]; modern || len(list["tools"].([]any)) != 1 {
		t.Fatalf("legacy tools/list: %v", list)
	}
	if replies[2]["result"].(map[string]any)["structuredContent"] == nil || replies[3]["result"] == nil {
		t.Fatalf("legacy call and ping: %v %v", replies[2], replies[3])
	}
	// An unknown legacy version gets the newest one this server speaks.
	if got := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-01-01"}}`)[0]["result"].(map[string]any)["protocolVersion"]; got != LegacyVersions[0] {
		t.Fatalf("initialize with an unknown version: %v", got)
	}
}
