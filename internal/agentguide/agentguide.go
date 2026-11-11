// Package agentguide holds the quick start for agents that the cicada binary
// prints with cicada agents, so an installed binary teaches itself with no
// repository checkout.
//
// The source is docs/manual/agent-quickstart.md. Go cannot embed a file
// outside the package's folder, so quickstart.md here is a copy of it, and
// TestQuickStartMatchesManualPage fails when the two differ. After editing
// the manual page, copy it with
//
//	CICADA_UPDATE_AGENT_GUIDE=1 go test ./internal/agentguide -run TestQuickStartMatchesManualPage
package agentguide

import _ "embed"

// QuickStart is the text of docs/manual/agent-quickstart.md.
//
//go:embed quickstart.md
var QuickStart string

// QuickStartBudget is the largest the quick start may be, in bytes: about
// 2,000 tokens at four bytes a token.
const QuickStartBudget = 8000

// Lookup is a command an agent runs when it needs more than the quick start
// gives. Command is written as the quick start writes it, with placeholders
// in angle brackets; Summary says what it answers.
type Lookup struct {
	Command string `json:"command"`
	Summary string `json:"summary"`
}

// Lookups are the quick start's on-demand commands, in the order the page
// names them. TestLookupsAreInTheQuickStart requires each Command to appear
// in the page.
var Lookups = []Lookup{
	{Command: "cicada intents --json --index", Summary: "every edit intent kind with its one-line meaning"},
	{Command: "cicada intents --json <kind>", Summary: "one intent kind: its fields, which are required, their units and allowed values, and examples that apply accepts"},
	{Command: "cicada symbols --json <score>", Summary: "the names a score defines: tracks, patterns, scenes"},
	{Command: "cicada explain <score> <track>.<control>", Summary: "one control of a track: its value in that score and where the value comes from"},
	{Command: "cicada params", Summary: "the controls Cicada knows for its voices, effects and mixer, each with its path, the engines that have it (voices), its unit and its range; a long list"},
	{Command: "cicada diagnostics --json <code>", Summary: "what a diagnostic code means and how to fix it"},
	{Command: "cicada help --json <command>", Summary: "a command's usage and flags"},
}
