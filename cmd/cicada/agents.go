package main

import (
	"fmt"
	"io"
	"strings"

	"m31labs.dev/cicada/internal/agentguide"
)

const agentsUsage = "usage: cicada agents [reference] [--json]"

// agentsSchema names the format of cicada agents --json.
const agentsSchema = "cicada.agents.v1"

// agentsDocument is cicada agents --json: the page asked for (the quick start
// unless reference is named) as guide, its size in bytes, the commands an
// agent runs to look up what the page leaves out, and every page the binary
// carries.
type agentsDocument struct {
	Schema     string              `json:"schema"`
	Page       string              `json:"page"`
	Guide      string              `json:"guide"`
	GuideBytes int                 `json:"guide_bytes"`
	Commands   []agentguide.Lookup `json:"commands"`
	Pages      []agentsPageJSON    `json:"pages"`
}

// agentsPageJSON is one page cicada agents prints: its name (the argument
// that prints it), its title and its size in bytes.
type agentsPageJSON struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Bytes int    `json:"bytes"`
}

// agentsPages are the pages the binary carries: the quick start, which
// cicada agents prints with no argument, and the core reference, which
// cicada agents reference prints.
var agentsPages = []struct{ name, text string }{
	{"quickstart", agentguide.QuickStart},
	{"reference", agentguide.Reference},
}

// agentsCommand prints the quick start for agents, or the core reference,
// which the binary carries, or with --json the cicada.agents.v1 document. It
// returns the exit status: 0, or 2 for a usage error.
func agentsCommand(args []string, stdout, stderr io.Writer) int {
	asJSON := hasJSONFlag(args)
	page := "quickstart"
	usage := func() int {
		if asJSON {
			writeAgentJSON(stdout, jsonErrorDocument{Schema: agentsSchema, Error: jsonError{Code: "usage", Message: agentsUsage}})
		}
		fmt.Fprintln(stderr, agentsUsage)
		return 2
	}
	named := false
	for _, arg := range args {
		switch {
		case arg == "--json" || arg == "--json=true":
		case arg == "reference" && !named:
			page, named = arg, true
		default:
			return usage()
		}
	}
	text := agentguide.QuickStart
	if page == "reference" {
		text = agentguide.Reference
	}
	var err error
	if asJSON {
		document := agentsDocument{Schema: agentsSchema, Page: page, Guide: text, GuideBytes: len(text), Commands: agentguide.Lookups, Pages: []agentsPageJSON{}}
		for _, p := range agentsPages {
			title, _, _ := strings.Cut(p.text, "\n")
			document.Pages = append(document.Pages, agentsPageJSON{Name: p.name, Title: strings.TrimSpace(strings.TrimLeft(title, "#")), Bytes: len(p.text)})
		}
		err = writeAgentJSON(stdout, document)
	} else {
		_, err = io.WriteString(stdout, text)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
