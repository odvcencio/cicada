package edit

import (
	"strings"
	"testing"
)

const placementFixture = "cicada 2\ntrack bass acid {}\npattern pulse { 1 . 5 . }\narrange {\n  // Opening stays beside this placement.\n  place one bass pulse { at = @1.1.1 length = 4bars } // keep\n  place two bass pulse { at = @5.1.1 length = 4bars }\n  marker return { at = @5.1.1 }\n}\n"

func applyPlacement(t *testing.T, intent Intent) (*Result, error) {
	t.Helper()
	return Apply([]byte(placementFixture), Envelope{Version: EnvelopeVersion, Intents: []Intent{intent}}, Options{Compiler: parseCompiler{}})
}

func TestPlacementIntentsKeepTypedLiteralsAndLabelTheEdit(t *testing.T) {
	for _, c := range []struct {
		intent       Intent
		want, label  string
		notContained string
	}{
		{&UpdatePlacement{Entity: "placement:two", Settings: &PlacementSettings{At: "@6.1.1", Length: "3bars"}}, "place two bass pulse { at = @6.1.1 length = 3bars }", "Placement two updated", ""},
		{&CopyPlacement{Entity: "placement:one", Name: "one-copy", Settings: &PlacementSettings{At: "@9.1.1"}}, "place one-copy bass pulse { at = @9.1.1 length = 4bars }\n", "Placement one copied as one-copy", ""},
		{&RemovePlacement{Entity: "placement:two"}, "// Opening stays beside this placement.", "Placement two removed", "place two"},
		{&SetMarker{Entity: "marker:return", Settings: &PlacementSettings{At: "@7.1.1"}}, "marker return { at = @7.1.1 }", "Marker return moved", ""},
		{&AddMarker{Name: "end", Settings: &PlacementSettings{At: "@9.1.1"}}, "marker end { at = @9.1.1 }", "Marker end added", ""},
		{&RemoveMarker{Entity: "marker:return"}, "place two", "Marker return removed", "marker return"},
	} {
		result, err := applyPlacement(t, c.intent)
		if err != nil || !strings.Contains(string(result.Source), c.want) || result.Label != c.label || c.notContained != "" && strings.Contains(string(result.Source), c.notContained) {
			t.Fatalf("%s: %v %q (label %q)", c.intent.Kind(), err, result.Source, result.Label)
		}
	}
}

func TestPlacementIntentsRefuseTheWrongEntityKind(t *testing.T) {
	for _, intent := range []Intent{
		&UpdatePlacement{Entity: "marker:return", Settings: &PlacementSettings{At: "@6.1.1"}},
		&RemovePlacement{Entity: "pattern:pulse"},
		&SetMarker{Entity: "placement:one", Settings: &PlacementSettings{At: "@6.1.1"}},
		&RemoveMarker{Entity: "track:bass"},
	} {
		if _, err := applyPlacement(t, intent); err == nil || !strings.Contains(err.Error(), "entity") {
			t.Fatalf("%s: %v", intent.Kind(), err)
		}
	}
}
