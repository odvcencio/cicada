// Package mcpwire serves the Model Context Protocol over a newline-delimited
// JSON-RPC stream (the stdio transport): one message per line in, one per
// line out, nothing else on the output.
//
// It speaks both eras of the protocol. A request whose params carry the
// per-request _meta of revision 2026-07-28 is served statelessly, and
// server/discover answers which versions the server supports. An initialize
// request selects the legacy, handshake-based revisions (2025-06-18 and
// 2025-11-25) for the rest of the stream. Tools are the only capability;
// every tool call goes to the Server's Call function.
package mcpwire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ModernVersion is the stateless protocol revision this package serves.
const ModernVersion = "2026-07-28"

// LegacyVersions are the handshake-based revisions an initialize request may
// select, newest first.
var LegacyVersions = []string{"2025-11-25", "2025-06-18"}

// MaxMessage is the longest line the server reads: 4 MiB.
const MaxMessage = 4 << 20

// JSON-RPC and MCP error codes.
const (
	CodeParse              = -32700
	CodeInvalidRequest     = -32600
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternal           = -32603
	CodeUnsupportedVersion = -32022
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
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
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

// Serve reads requests from in until it closes and writes one response for
// each request (none for a notification) to out. Requests are answered in
// order. It returns nil when in ends.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), MaxMessage)
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
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: CodeParse, Message: "parse error: " + err.Error()}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification: initialized, cancelled; nothing to answer
		}
		result, failure := s.handle(&req, &legacy)
		reply := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: failure}
		if failure != nil {
			reply.Result = nil
		}
		if err := write(reply); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handle(req *request, legacy *string) (map[string]any, *rpcError) {
	if req.JSONRPC != "2.0" || req.Method == "" {
		return nil, &rpcError{Code: CodeInvalidRequest, Message: "not a JSON-RPC 2.0 request"}
	}
	var params struct {
		Meta            map[string]json.RawMessage `json:"_meta"`
		ProtocolVersion string                     `json:"protocolVersion"`
		Name            string                     `json:"name"`
		Arguments       json.RawMessage            `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{Code: CodeInvalidParams, Message: "params: " + err.Error()}
		}
	}
	if req.Method == "initialize" {
		version := LegacyVersions[0]
		for _, supported := range LegacyVersions {
			if params.ProtocolVersion == supported {
				version = supported
			}
		}
		*legacy = version
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
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
	result, failure := s.method(req.Method, params.Name, params.Arguments, modern)
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

func (s *Server) method(method, name string, arguments json.RawMessage, modern bool) (map[string]any, *rpcError) {
	switch method {
	case "server/discover":
		if !modern {
			break
		}
		return map[string]any{
			"supportedVersions": []string{ModernVersion},
			"capabilities":      map[string]any{"tools": map[string]any{"listChanged": false}},
			"instructions":      s.Instructions,
			"ttlMs":             3600000,
			"cacheScope":        "public",
		}, nil
	case "ping":
		if !modern {
			return map[string]any{}, nil
		}
	case "tools/list":
		result := map[string]any{"tools": s.Tools}
		if modern {
			result["ttlMs"], result["cacheScope"] = 3600000, "public"
		}
		return result, nil
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
		var structured any
		if err := json.Unmarshal(result.Document, &structured); err != nil {
			return nil, &rpcError{Code: CodeInternal, Message: "tool " + name + " returned no JSON document: " + err.Error()}
		}
		compact, _ := json.Marshal(structured)
		return map[string]any{
			"content":           []map[string]string{{"type": "text", "text": string(compact)}},
			"structuredContent": structured,
			"isError":           result.IsError,
		}, nil
	}
	return nil, &rpcError{Code: CodeMethodNotFound, Message: "method not found: " + method}
}
