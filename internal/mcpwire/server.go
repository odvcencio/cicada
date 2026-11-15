// Package mcpwire serves the Model Context Protocol over a newline-delimited
// JSON-RPC stream (the stdio transport): one message per line in, one per
// line out, nothing else on the output.
//
// It speaks both eras of the protocol. A request whose params carry the
// per-request _meta of revision 2026-07-28 is served statelessly, and
// server/discover answers which versions the server supports. An initialize
// request selects the legacy, handshake-based revisions (2025-06-18 and
// 2025-11-25) for the rest of the stream. The capabilities are tools and, when
// the Server lists any, resources; every tool call goes to the Server's Call
// function and every resource read to its Read function.
package mcpwire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"
)

// ModernVersion is the stateless protocol revision this package serves.
const ModernVersion = "2026-07-28"

// LegacyVersions are the handshake-based revisions an initialize request may
// select, newest first.
var LegacyVersions = []string{"2025-11-25", "2025-06-18"}

// MaxMessage is the longest line the server reads: 4 MiB. A longer line is
// skipped and answered with CodeInvalidRequest.
const MaxMessage = 4 << 20

// JSON-RPC and MCP error codes.
const (
	CodeParse              = -32700
	CodeInvalidRequest     = -32600
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternal           = -32603
	CodeUnsupportedVersion = -32022
	// CodeLegacyResourceNotFound is the legacy revisions' code for an unknown
	// resource; 2026-07-28 uses CodeInvalidParams instead.
	CodeLegacyResourceNotFound = -32002
)

// Tool is one tool the server lists, in the order the server lists them.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  *Annotations    `json:"annotations,omitempty"`
}

// Annotations are a tool's behaviour hints. Clients treat them as untrusted.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// Resource is one resource the server lists, in the order it lists them.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Size        int    `json:"size,omitempty"`
}

// ResourceContents is what a resource read returns: one text.
type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
}

// ErrResourceNotFound is what Read returns for a URI the server does not
// serve.
var ErrResourceNotFound = errors.New("resource not found")

// Result is what a tool returns: a JSON document, which goes out as
// structuredContent and, compacted, as the one text block, and whether the
// tool refused (isError).
type Result struct {
	Document json.RawMessage
	IsError  bool
}

// ToolError is returned by Call for an unknown tool or arguments the tool
// cannot read; it becomes a JSON-RPC error. A tool that runs and refuses
// returns a Result with IsError instead.
type ToolError struct {
	Code    int
	Message string
}

func (e *ToolError) Error() string { return e.Message }

// Server describes what the server offers.
type Server struct {
	Name, Version string
	Instructions  string
	Tools         []Tool
	Call          func(name string, arguments json.RawMessage) (Result, error)
	Resources     []Resource
	Read          func(uri string) (ResourceContents, error)
	// Log receives the stack of a call that panicked; nil discards it.
	Log io.Writer
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	metaVersion      = "io.modelcontextprotocol/protocolVersion"
	metaCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo   = "io.modelcontextprotocol/serverInfo"
)

var nullID = json.RawMessage("null")

// Serve reads requests from in until it closes and writes one response for
// each request (none for a notification or a response) to out. Requests are
// answered in order. A line that is not a request gets an error reply and the
// server goes on; a tool or resource that panics fails only its own request.
// It returns nil when in ends.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReaderSize(in, 64<<10)
	var mu sync.Mutex
	write := func(message any) error {
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		_, err = out.Write(append(data, '\n'))
		return err
	}
	legacy := "" // the legacy version an initialize request selected
	for {
		line, tooLong, err := readLine(reader)
		if tooLong {
			reply := response{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: CodeInvalidRequest, Message: fmt.Sprintf("message longer than %d bytes; it was skipped", MaxMessage)}}
			if err := write(reply); err != nil {
				return err
			}
		} else if len(bytes.TrimSpace(line)) > 0 {
			if reply := s.serveLine(line, &legacy); reply != nil {
				if err := write(reply); err != nil {
					return err
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLine reads one line without its end. A line over MaxMessage is read to
// its end and dropped, and tooLong is true.
func readLine(reader *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, isPrefix, readErr := reader.ReadLine()
		if !tooLong {
			if len(line)+len(chunk) > MaxMessage {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if readErr != nil || !isPrefix {
			return line, tooLong, readErr
		}
	}
}

// serveLine answers one line: a reply, or nil for a notification or a
// response.
func (s *Server) serveLine(line []byte, legacy *string) *response {
	var message map[string]json.RawMessage
	if err := json.Unmarshal(line, &message); err != nil {
		if json.Valid(line) {
			return &response{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: CodeInvalidRequest, Message: "not a JSON-RPC request object"}}
		}
		return &response{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: CodeParse, Message: "parse error: " + err.Error()}}
	}
	rawID, hasID := message["id"]
	id := nullID
	if hasID {
		if !validID(rawID) {
			return &response{JSONRPC: "2.0", ID: nullID, Error: &rpcError{Code: CodeInvalidRequest, Message: "id must be a string or a number"}}
		}
		id = rawID
	}
	rawMethod, hasMethod := message["method"]
	if !hasMethod {
		_, isResult := message["result"]
		_, isError := message["error"]
		if hasID && (isResult || isError) {
			return nil // a response; this server sends no requests
		}
		return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: CodeInvalidRequest, Message: "not a JSON-RPC 2.0 request: no method"}}
	}
	var method, version string
	if json.Unmarshal(rawMethod, &method) != nil || method == "" || json.Unmarshal(message["jsonrpc"], &version) != nil || version != "2.0" {
		return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: CodeInvalidRequest, Message: "not a JSON-RPC 2.0 request: jsonrpc must be \"2.0\" and method a string"}}
	}
	if !hasID {
		return nil // a notification: initialized, cancelled; nothing to answer
	}
	result, failure := s.protect(method, message["params"], legacy)
	reply := &response{JSONRPC: "2.0", ID: id, Result: result, Error: failure}
	if failure != nil {
		reply.Result = nil
	}
	return reply
}

// validID reports whether an id is a JSON string or number.
func validID(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	}
	return false
}

// protect runs one request and turns a panic into an internal error for that
// request alone.
func (s *Server) protect(method string, params json.RawMessage, legacy *string) (result map[string]any, failure *rpcError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			label := method
			var named struct{ Name, URI string }
			if json.Unmarshal(params, &named) == nil && named.Name+named.URI != "" {
				label += " " + named.Name + named.URI
			}
			if s.Log != nil {
				fmt.Fprintf(s.Log, "mcp: %s panicked: %v\n%s", label, recovered, debug.Stack())
			}
			result, failure = nil, &rpcError{Code: CodeInternal, Message: fmt.Sprintf("internal error in %s: %v", label, recovered)}
		}
	}()
	return s.handle(method, params, legacy)
}

func (s *Server) handle(method string, rawParams json.RawMessage, legacy *string) (map[string]any, *rpcError) {
	var params struct {
		Meta            map[string]json.RawMessage `json:"_meta"`
		ProtocolVersion string                     `json:"protocolVersion"`
		Name            string                     `json:"name"`
		Arguments       json.RawMessage            `json:"arguments"`
		URI             string                     `json:"uri"`
	}
	if len(rawParams) > 0 && string(rawParams) != "null" {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return nil, &rpcError{Code: CodeInvalidParams, Message: "params: " + err.Error()}
		}
	}
	if method == "initialize" {
		version := LegacyVersions[0]
		for _, supported := range LegacyVersions {
			if params.ProtocolVersion == supported {
				version = supported
			}
		}
		*legacy = version
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    s.capabilities(),
			"serverInfo":      map[string]string{"name": s.Name, "version": s.Version},
			"instructions":    s.Instructions,
		}, nil
	}
	modern := false
	if raw, ok := params.Meta[metaVersion]; ok {
		var version string
		if err := json.Unmarshal(raw, &version); err != nil {
			return nil, &rpcError{Code: CodeInvalidParams, Message: "_meta " + metaVersion + " is not a string"}
		}
		if version != ModernVersion {
			return nil, &rpcError{Code: CodeUnsupportedVersion, Message: "Unsupported protocol version", Data: map[string]any{"supported": s.supported(), "requested": version}}
		}
		if _, ok := params.Meta[metaCapabilities]; !ok {
			return nil, &rpcError{Code: CodeInvalidParams, Message: "_meta lacks " + metaCapabilities}
		}
		modern = true
	} else if *legacy == "" {
		return nil, &rpcError{Code: CodeInvalidParams, Message: fmt.Sprintf("the request has no _meta %s; this server supports %v, or send initialize first for %v", metaVersion, ModernVersion, LegacyVersions)}
	}
	result, failure := s.method(method, params.Name, params.Arguments, params.URI, modern)
	if failure != nil || !modern {
		return result, failure
	}
	result["resultType"] = "complete"
	meta, _ := result["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta[metaServerInfo] = map[string]string{"name": s.Name, "version": s.Version}
	result["_meta"] = meta
	return result, nil
}

func (s *Server) supported() []string { return append([]string{ModernVersion}, LegacyVersions...) }

func (s *Server) capabilities() map[string]any {
	capabilities := map[string]any{"tools": map[string]any{"listChanged": false}}
	if len(s.Resources) > 0 {
		capabilities["resources"] = map[string]any{"listChanged": false, "subscribe": false}
	}
	return capabilities
}

// cacheable adds the 2026-07-28 cache hints to a list or read result: one
// hour, public. Everything this server lists is built into the binary.
func cacheable(result map[string]any, modern bool) map[string]any {
	if modern {
		result["ttlMs"], result["cacheScope"] = 3600000, "public"
	}
	return result
}

func (s *Server) method(method, name string, arguments json.RawMessage, uri string, modern bool) (map[string]any, *rpcError) {
	switch method {
	case "server/discover":
		if !modern {
			break
		}
		return cacheable(map[string]any{
			"supportedVersions": []string{ModernVersion},
			"capabilities":      s.capabilities(),
			"instructions":      s.Instructions,
		}, true), nil
	case "ping":
		if !modern {
			return map[string]any{}, nil
		}
	case "tools/list":
		return cacheable(map[string]any{"tools": s.Tools}, modern), nil
	case "tools/call":
		if name == "" {
			return nil, &rpcError{Code: CodeInvalidParams, Message: "tools/call needs params.name"}
		}
		if len(arguments) == 0 || string(arguments) == "null" {
			arguments = json.RawMessage("{}")
		}
		result, err := s.Call(name, arguments)
		if err != nil {
			var tool *ToolError
			if errors.As(err, &tool) {
				return nil, &rpcError{Code: tool.Code, Message: tool.Message}
			}
			return nil, &rpcError{Code: CodeInternal, Message: err.Error()}
		}
		var structured map[string]any
		if err := json.Unmarshal(result.Document, &structured); err != nil {
			return nil, &rpcError{Code: CodeInternal, Message: "tool " + name + " returned no JSON object: " + err.Error()}
		}
		compact, _ := json.Marshal(structured)
		return map[string]any{
			"content":           []map[string]string{{"type": "text", "text": string(compact)}},
			"structuredContent": structured,
			"isError":           result.IsError,
		}, nil
	case "resources/list":
		if len(s.Resources) == 0 {
			break
		}
		return cacheable(map[string]any{"resources": s.Resources}, modern), nil
	case "resources/read":
		if len(s.Resources) == 0 {
			break
		}
		if uri == "" {
			return nil, &rpcError{Code: CodeInvalidParams, Message: "resources/read needs params.uri"}
		}
		contents, err := s.Read(uri)
		if errors.Is(err, ErrResourceNotFound) {
			code := CodeInvalidParams
			if !modern {
				code = CodeLegacyResourceNotFound
			}
			return nil, &rpcError{Code: code, Message: "resource not found: " + uri, Data: map[string]string{"uri": uri}}
		}
		if err != nil {
			return nil, &rpcError{Code: CodeInternal, Message: err.Error()}
		}
		return cacheable(map[string]any{"contents": []ResourceContents{contents}}, modern), nil
	}
	return nil, &rpcError{Code: CodeMethodNotFound, Message: "method not found: " + method}
}
