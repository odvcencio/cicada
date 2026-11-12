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
			if name == "explode" {
				panic("boom")
			}
			if name != "echo" {
				return Result{}, &ToolError{Code: CodeInvalidParams, Message: "unknown tool: " + name}
			}
			return Result{Document: json.RawMessage(`{"schema":"test.v1","got":` + string(arguments) + `}`), IsError: strings.Contains(string(arguments), "fail")}, nil
		},
		Resources: []Resource{{URI: "test://guide", Name: "guide", MimeType: "text/markdown"}},
		Read: func(uri string) (ResourceContents, error) {
			if uri != "test://guide" {
				return ResourceContents{}, ErrResourceNotFound
			}
			return ResourceContents{URI: uri, MimeType: "text/markdown", Text: "# Guide\n"}, nil
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

// TestResources lists and reads a resource in both eras; an unknown URI is
// -32602 in 2026-07-28 and -32002 in the legacy revisions.
func TestResources(t *testing.T) {
	replies := exchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list","params":{`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"test://guide",`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"test://nosuch",`+modernMeta+`}}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{`+modernMeta+`}}`,
	)
	if len(replies) != 5 {
		t.Fatalf("%d replies: %v", len(replies), replies)
	}
	if replies[0]["result"].(map[string]any)["capabilities"].(map[string]any)["resources"] == nil {
		t.Fatalf("discover lacks the resources capability: %v", replies[0])
	}
	list := replies[1]["result"].(map[string]any)
	if list["resources"].([]any)[0].(map[string]any)["uri"] != "test://guide" || list["cacheScope"] != "public" || list["resultType"] != "complete" {
		t.Fatalf("resources/list: %v", list)
	}
	read := replies[2]["result"].(map[string]any)
	contents := read["contents"].([]any)[0].(map[string]any)
	if contents["text"] != "# Guide\n" || contents["mimeType"] != "text/markdown" || read["ttlMs"] == nil {
		t.Fatalf("resources/read: %v", read)
	}
	if errorCode(replies[3]) != CodeInvalidParams || errorCode(replies[4]) != CodeInvalidParams {
		t.Fatalf("an unknown and a missing uri: %v %v", replies[3], replies[4])
	}
	legacy := exchange(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"test://guide"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"test://nosuch"}}`,
	)
	if legacy[0]["result"].(map[string]any)["capabilities"].(map[string]any)["resources"] == nil || legacy[1]["result"] == nil || errorCode(legacy[2]) != CodeLegacyResourceNotFound {
		t.Fatalf("legacy resources: %v", legacy)
	}
	// A server with no resources does not offer the methods.
	plain := testServer()
	plain.Resources = nil
	var out strings.Builder
	if err := plain.Serve(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{`+modernMeta+`}}`+"\n"), &out); err != nil || !strings.Contains(out.String(), `"code":-32601`) {
		t.Fatalf("resources/list without resources: %v %s", err, out.String())
	}
}

// TestMalformedInputGetsTheRightError: invalid JSON is a parse error;
// valid JSON that is not a request object, a null or object id, a method
// that is not a string, and a missing method are invalid requests, with the
// id when it can be read; a response from the client gets no reply.
func TestMalformedInputGetsTheRightError(t *testing.T) {
	replies := exchange(t,
		`{"jsonrpc":`,
		`[1,2]`,
		`123`,
		`{"jsonrpc":"2.0","id":7,"method":5}`,
		`{"jsonrpc":"2.0","id":null,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"tools/list"}`,
		`{"jsonrpc":"1.0","id":8,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":9}`,
		`{"jsonrpc":"2.0","id":10,"result":{}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/list","params":{`+modernMeta+`}}`,
	)
	want := []struct {
		code float64
		id   any
	}{{CodeParse, nil}, {CodeInvalidRequest, nil}, {CodeInvalidRequest, nil}, {CodeInvalidRequest, float64(7)}, {CodeInvalidRequest, nil}, {CodeInvalidRequest, nil}, {CodeInvalidRequest, float64(8)}, {CodeInvalidRequest, float64(9)}, {0, float64(11)}}
	if len(replies) != len(want) {
		t.Fatalf("%d replies, want %d (none for the response): %v", len(replies), len(want), replies)
	}
	for i, w := range want {
		if errorCode(replies[i]) != w.code || replies[i]["id"] != w.id {
			t.Errorf("reply %d: %v, want code %v id %v", i, replies[i], w.code, w.id)
		}
	}
}

// TestPanicFailsOnlyItsRequest: a tool that panics gets -32603 and the
// server answers the next request.
func TestPanicFailsOnlyItsRequest(t *testing.T) {
	var log strings.Builder
	server := testServer()
	server.Log = &log
	var out strings.Builder
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"explode",` + modernMeta + `}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{},` + modernMeta + `}}` + "\n"
	if err := server.Serve(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"code":-32603`) || !strings.Contains(lines[0], "boom") || !strings.Contains(lines[1], `"isError":false`) {
		t.Fatalf("after a panic: %q", out.String())
	}
	if !strings.Contains(log.String(), "explode") || !strings.Contains(log.String(), "goroutine") {
		t.Fatalf("the panic's stack was not logged: %q", log.String())
	}
}

// TestOverlongLineIsSkipped: a line over MaxMessage gets -32600 with a null
// id, and the server reads the next line.
func TestOverlongLineIsSkipped(t *testing.T) {
	long := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"pad":"` + strings.Repeat("x", MaxMessage) + `"},` + modernMeta + `}}`
	replies := exchange(t, long, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{`+modernMeta+`}}`)
	if len(replies) != 2 || errorCode(replies[0]) != CodeInvalidRequest || replies[0]["id"] != nil || replies[1]["result"] == nil {
		t.Fatalf("an overlong line: %v", replies)
	}
	// The last line need not end in a newline.
	var out strings.Builder
	if err := testServer().Serve(strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{`+modernMeta+`}}`), &out); err != nil || !strings.Contains(out.String(), `"id":3`) {
		t.Fatalf("a last line without a newline: %v %q", err, out.String())
	}
}
