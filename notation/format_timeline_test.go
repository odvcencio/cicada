package notation

import (
	"strings"
	"testing"
)

// An arrangement keeps one tempo step, placement or marker per line, with each
// placement's fields on its own line, and an automation lane keeps one point
// per line. Formatting the result again changes nothing.
func TestFormatKeepsOneTimelineEntryPerLine(t *testing.T) {
	source := "cicada 2\ntrack bass acid {}\npattern pulse acid { 1 . 5 . }\narrange { tempo @1.1.1 120 tempo @3.1.1 96 place a bass pulse { at = @1.1.1 length = 2bars } marker drop { at = @3.1.1 } place b bass pulse { at = @3.1.1 length = 2bars } }\nautomate bass.cutoff { @1.1.1 400Hz @3.1.1 1200Hz smooth }\n"
	document, err := ParseDocument([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\n  tempo @1.1.1 120\n  tempo @3.1.1 96\n",
		"\n  place a bass pulse { at = @1.1.1 length = 2bars }\n",
		"\n  marker drop { at = @3.1.1 }\n",
		"\n  @1.1.1 400Hz\n  @3.1.1 1200Hz smooth\n",
	} {
		if !strings.Contains(string(formatted), want) {
			t.Errorf("formatted source lacks %q:\n%s", want, formatted)
		}
	}
	again, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Format(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(twice) != string(formatted) {
		t.Fatalf("formatting is not idempotent:\n%s\n---\n%s", formatted, twice)
	}
}

// recall takes the position after it, so it starts its own line; a placement
// whose pattern is named like a keyword stays on one line.
func TestFormatKeepsRecallAndKeywordNamesInPlace(t *testing.T) {
	source := "cicada 2\ntrack bass acid {}\npattern marker acid { 1 . 5 . }\narrange { tempo @1.1.1 120 place a bass marker { at = @1.1.1 length = 2bars } }\nautomate bass.cutoff { @1.1.1 400Hz @5.1.1 2400Hz exponential recall @3.1.1 }\n"
	document, err := ParseDocument([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\n  place a bass marker { at = @1.1.1 length = 2bars }\n",
		"\n  @5.1.1 2400Hz exponential\n  recall @3.1.1\n",
	} {
		if !strings.Contains(string(formatted), want) {
			t.Fatalf("formatted source lacks %q:\n%s", want, formatted)
		}
	}
}
