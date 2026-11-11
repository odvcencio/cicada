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
