package notation

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/diagnostics"
)

// TestEveryCodePrintsOnce is baobab's finding: a kit lane that read mod()
// printed "error CICADA-PARAM: CICADA-UNSUPPORTED: ...", two codes. For every
// pair of codes in the diagnostics table, a diagnostic reported under one
// code whose message starts with the other prints one code, the one the
// message names, and SingleCode makes that its Code.
func TestEveryCodePrintsOnce(t *testing.T) {
	codes := diagnostics.Catalog()
	if len(codes) < 30 {
		t.Fatalf("the diagnostics table has only %d codes", len(codes))
	}
	for _, outer := range codes {
		for _, inner := range codes {
			d := Diagnostic{Code: outer.Code, Severity: "error", Message: inner.Code + ": the cause", Position: Position{Line: 3, Column: 2}}
			printed := d.Error()
			if got := strings.Count(printed, "CICADA-"); got != 1 {
				t.Fatalf("%s wrapping %s prints %d codes: %q", outer.Code, inner.Code, got, printed)
			}
			if want := "3:2: error " + inner.Code + ": the cause"; printed != want {
				t.Fatalf("%s wrapping %s prints %q, want %q", outer.Code, inner.Code, printed, want)
			}
			if single := d.SingleCode(); single.Code != inner.Code || single.Message != "the cause" || d.Text() != "the cause" {
				t.Fatalf("%s wrapping %s: SingleCode %+v, Text %q", outer.Code, inner.Code, single, d.Text())
			}
		}
	}
	// A message that only mentions a code later keeps its own code.
	d := Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: "see CICADA-UNIT: not a prefix"}
	if d.SingleCode().Code != "CICADA-PARAM" {
		t.Fatal("a code inside the message replaced the diagnostic's code")
	}
}

// TestSecondReturnReportsAtItsDeclaration is banyan's finding: a second
// reverb reported at line 1, column 1. Each extra reverb, delay or tape now
// reports at its own declaration and names the effect that holds the return.
func TestSecondReturnReportsAtItsDeclaration(t *testing.T) {
	source := "cicada 2\ntempo 120\nfx hall reverb { decay = 4s }\nfx echo tape {}\nfx room reverb { decay = 1s }\nfx slap delay {}\ntrack a acid { send hall = -6dB }\npattern p { c3 . . . }\nscene main { a = p }\nsong { main*2 }\n"
	score, ds := Parse([]byte(source))
	if score == nil {
		t.Fatal(ds)
	}
	want := map[int]string{
		5: "multiple reverb instances are not implemented; fx hall is the score's reverb return already",
		6: "multiple delay or tape instances are not implemented: a tape echo is the delay return; fx echo is the score's delay return already",
	}
	for _, d := range Validate(score) {
		if !strings.HasPrefix(d.Message, "multiple ") {
			continue
		}
		if want[d.Position.Line] != d.Message || d.Position.Column != 1 || d.Code != "CICADA-UNSUPPORTED" {
			t.Fatalf("diagnostic %s", d.Error())
		}
		delete(want, d.Position.Line)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics at lines %v", want)
	}
}
