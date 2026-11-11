package agentguide

import (
	"os"
	"strings"
	"testing"
)

const manualPage = "../../docs/manual/agent-quickstart.md"

// TestQuickStartMatchesManualPage fails when the embedded copy differs from
// docs/manual/agent-quickstart.md. CICADA_UPDATE_AGENT_GUIDE=1 writes the
// copy instead.
func TestQuickStartMatchesManualPage(t *testing.T) {
	page, err := os.ReadFile(manualPage)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CICADA_UPDATE_AGENT_GUIDE") == "1" {
		if err := os.WriteFile("quickstart.md", page, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if string(page) != QuickStart {
		t.Fatalf("internal/agentguide/quickstart.md differs from %s; run CICADA_UPDATE_AGENT_GUIDE=1 go test ./internal/agentguide -run TestQuickStartMatchesManualPage", manualPage)
	}
}

func TestQuickStartFitsItsBudget(t *testing.T) {
	if len(QuickStart) > QuickStartBudget {
		t.Fatalf("the quick start is %d bytes; its budget is %d", len(QuickStart), QuickStartBudget)
	}
	if !strings.HasPrefix(QuickStart, "# ") {
		t.Fatal("the quick start does not start with its title")
	}
}

// TestLookupsAreInTheQuickStart keeps cicada agents --json and the page
// naming the same on-demand commands, in the same order.
func TestLookupsAreInTheQuickStart(t *testing.T) {
	at := 0
	for _, lookup := range Lookups {
		if lookup.Command == "" || lookup.Summary == "" {
			t.Fatalf("incomplete lookup %+v", lookup)
		}
		index := strings.Index(QuickStart[at:], "`"+lookup.Command+"`")
		if index < 0 {
			t.Fatalf("the quick start does not name `%s` after the lookups before it", lookup.Command)
		}
		at += index
	}
}

func TestBuildHelpers(t *testing.T) {
	for _, tc := range []struct{ dir, target, want string }{
		{"docs/manual", "docs/audio/mix-chain.md", "../audio/mix-chain.md"},
		{"docs/manual", "docs/manual/keys.md", "keys.md"},
		{".", "docs/manual/keys.md", "docs/manual/keys.md"},
		{"docs/manual", "examples/x.cicada", "../../examples/x.cicada"},
	} {
		if got := relative(tc.dir, tc.target); got != tc.want {
			t.Errorf("relative(%q, %q) = %q, want %q", tc.dir, tc.target, got, tc.want)
		}
	}
	if got := rebaseLinks("see [a](keys.md#x) and [b](https://e.org) and [c](#here)", "docs/manual", "."); got != "see [a](docs/manual/keys.md#x) and [b](https://e.org) and [c](#here)" {
		t.Errorf("rebaseLinks: %q", got)
	}
	page := "# T\n\n## A\n\ntext\n\n```sh\n# not a heading\n```\n\n### A.1\n\nmore\n\n## B\n\nlast\n"
	if got, err := section(page, "## A"); err != nil || got != "## A\n\ntext\n\n```sh\n# not a heading\n```\n\n### A.1\n\nmore\n" {
		t.Errorf("section ## A: %q %v", got, err)
	}
	if got, err := section(page, "# T"); err != nil || got != "# T\n" {
		t.Errorf("section # T: %q %v", got, err)
	}
	if _, err := section(page, "## C"); err == nil {
		t.Error("section of a missing heading succeeded")
	}
}
