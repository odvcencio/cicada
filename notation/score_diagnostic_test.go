package notation

import (
	"strings"
	"testing"
)

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

// A send path that names the effect, as a track's send line does, gets a hint
// with the slot path to use instead.
func TestSendPathNamedByEffectGetsAHint(t *testing.T) {
	source := "cicada 2\ntempo 120\nfx echo tape { time = 1/8 }\nfx hall reverb { size = 0.5 }\ntrack keys acid { send echo = 0.2 send hall = 0.2 }\npattern p acid { 1 . 3 . }\nscene s { keys = p keys.send.echo = 0.1 keys.send.hall = 0.1 }\nsong { s }\n"
	score, ds := Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	ds = append(ds, Validate(score)...)
	want := map[string]bool{"the send to echo is send.delay": false, "the send to hall is send.reverb": false}
	for _, d := range ds {
		for text := range want {
			if strings.Contains(d.Message, text) {
				want[text] = true
			}
		}
	}
	for text, seen := range want {
		if !seen {
			t.Errorf("no diagnostic says %q: %+v", text, ds)
		}
	}
}
