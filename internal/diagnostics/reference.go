package diagnostics

import (
	"bytes"
	"fmt"
	"strings"
)

// ReferencePage renders docs/manual/diagnostics.md from the catalog.
func ReferencePage() []byte {
	var page bytes.Buffer
	page.WriteString("# Diagnostic codes\n\n")
	page.WriteString("<!-- Generated from internal/diagnostics/catalog.go. Do not edit; run\n")
	page.WriteString("CICADA_UPDATE_DIAGNOSTICS_DOC=1 go test ./internal/diagnostics -run TestDiagnosticsReferenceIsCurrent -->\n\n")
	page.WriteString("Every code cicada can report, with its severity, where it is reported, what\n")
	page.WriteString("it means and how to fix it. `cicada diagnostics` prints the same list, and\n")
	page.WriteString("`cicada diagnostics --json` writes it as JSON ([agent formats](agents.md#diagnostics---json)).\n")
	page.WriteString("A test fails when the code emits a code this list lacks.\n\n")
	page.WriteString("Where a code is reported:\n\n")
	page.WriteString("- **score**: validating a score. `cicada check` prints it with its source line,\n")
	page.WriteString("  `cicada check --json` gives its position as data, and `validate`, `render` and\n")
	page.WriteString("  the language server report it too.\n")
	page.WriteString("- **command**: the error of a command, such as `lib`, `bundle`, `convert`,\n")
	page.WriteString("  `director` or the export settings of `render`.\n")
	page.WriteString("- **studio**: responses of Studio, the browser host or the public demo.\n")
	page.WriteString("- **edit**: refusals of edit intents, from `cicada apply` and Studio.\n\n")
	page.WriteString("| Code | Severity | Where | Meaning | Fix |\n")
	page.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, entry := range Catalog() {
		fmt.Fprintf(&page, "| `%s` | %s | %s | %s | %s |\n", entry.Code, strings.Join(entry.Severities, ", "), strings.Join(entry.Scopes, ", "), cell(entry.Meaning), cell(entry.Fix))
	}
	for _, entry := range Catalog() {
		if len(entry.Reasons) == 0 {
			continue
		}
		fmt.Fprintf(&page, "\n## %s reasons\n\n", entry.Code)
		fmt.Fprintf(&page, "A `%s` refusal carries one of these values in `reason`.\n\n", entry.Code)
		page.WriteString("| `reason` | Meaning |\n| --- | --- |\n")
		for _, reason := range entry.Reasons {
			fmt.Fprintf(&page, "| `%s` | %s |\n", reason.Reason, cell(reason.Meaning))
		}
	}
	return page.Bytes()
}

// cell escapes the characters that would end a Markdown table cell.
func cell(text string) string {
	return strings.ReplaceAll(text, "|", `\|`)
}
