package notation

import "testing"

// A wrapped error that starts with its own code prints the code once.
func TestDiagnosticPrintsItsCodeOnce(t *testing.T) {
	ds := []Diagnostic{{Code: "CICADA-PARAM", Severity: "error", Message: "CICADA-PARAM: chord too large"}}
	if got, want := ds[0].Error(), "0:0: error CICADA-PARAM: chord too large"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	LocateDiagnostics(ds, Position{File: "main.cicada"})
	if ds[0].Message != "chord too large" {
		t.Fatalf("message kept its code: %q", ds[0].Message)
	}
	other := Diagnostic{Code: "CICADA-UNIT", Message: "CICADA-PARAM: kept"}
	if trimCode(other.Code, other.Message) != "CICADA-PARAM: kept" {
		t.Fatal("a different leading code was removed")
	}
}
