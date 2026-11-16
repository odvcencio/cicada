package project

import (
	"testing"

	"m31labs.dev/cicada/notation"
)

// An export tail outside the renderer's range is one rule: the score's export
// block (cicada check) and the JSON form refuse it with the same message,
// which names the export and the value, and the same code, CICADA-PARAM. The
// JSON form used to say CICADA-UNSUPPORTED without the value.
func TestExportTailOutOfRangeIsOneRule(t *testing.T) {
	// Only the export block matters; the score has no track, which the parser
	// also reports.
	score, _ := notation.Parse([]byte("cicada 2\ntempo 120\nexport over { tail = 61s }\n"))
	if score == nil || len(score.Exports) != 1 {
		t.Fatalf("parse: %+v", score)
	}
	_, scoreErr := compileExport(score.Exports[0])
	ms := 61_000.0
	jsonErr := validateExports([]Export{{ID: "over", Tail: &Value{Unit: "ms", Number: &ms}}})
	want := "export over tail 61s is outside 0 to 60s"
	if scoreErr == nil || jsonErr == nil || scoreErr.Error() != want || jsonErr.Error() != want {
		t.Fatalf("score form %v, JSON form %v, want both %q", scoreErr, jsonErr, want)
	}
	if code := projectDiagnosticCode(jsonErr); code != "CICADA-PARAM" {
		t.Fatalf("JSON form code %s, want CICADA-PARAM, the code check gives", code)
	}
}
