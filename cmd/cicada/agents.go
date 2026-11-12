package main

import (
	"fmt"
	"io"

	"m31labs.dev/cicada/internal/agentguide"
)

const agentsUsage = "usage: cicada agents [--json]"

// agentsSchema names the format of cicada agents --json.
const agentsSchema = "cicada.agents.v1"

// agentsDocument is cicada agents --json: the quick start's text, its size in
// bytes, and the commands an agent runs to look up what the quick start
// leaves out.
type agentsDocument struct {
	Schema     string              `json:"schema"`
	Guide      string              `json:"guide"`
	GuideBytes int                 `json:"guide_bytes"`
	Commands   []agentguide.Lookup `json:"commands"`
}

// agentsCommand prints the quick start for agents, which the binary carries,
// or with --json the cicada.agents.v1 document. It returns the exit status:
// 0, or 2 for a usage error.
func agentsCommand(args []string, stdout, stderr io.Writer) int {
	asJSON := hasJSONFlag(args)
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" {
			continue
		}
		if asJSON {
			writeAgentJSON(stdout, jsonErrorDocument{Schema: agentsSchema, Error: jsonError{Code: "usage", Message: agentsUsage}})
		}
		fmt.Fprintln(stderr, agentsUsage)
		return 2
	}
	var err error
	if asJSON {
		err = writeAgentJSON(stdout, agentsDocument{Schema: agentsSchema, Guide: agentguide.QuickStart, GuideBytes: len(agentguide.QuickStart), Commands: agentguide.Lookups})
	} else {
		_, err = io.WriteString(stdout, agentguide.QuickStart)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
