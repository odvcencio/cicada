package notation

import (
	"bytes"
	"testing"
)

const arrangeFixture = `cicada 2
track bass acid {}
pattern hits { c3 . c4 . }
arrange {
  // Keep this spelling and comment.
  place bass-1 bass hits { at = @2.3.4 length = 1/2 }
  marker chorus { at = @5.1.1 }
}
`

func TestArrangementExactSourceAndMusicalTime(t *testing.T) {
	source := []byte(arrangeFixture)
	s, ds := Parse(source)
	if parseHasErrors(ds) {
		t.Fatal(ds)
	}
	p := s.Arrange.Placements[0]
	if p.AtTick != 6480 || p.LengthTicks != 1920 || s.Arrange.Markers[0].AtTick != 15360 {
		t.Fatalf("wrong ticks: %+v", s.Arrange)
	}
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Print(doc), source) {
		t.Fatal("source changed")
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	after, ds := Parse(formatted)
	if parseHasErrors(ds) {
		t.Fatal(string(formatted), ds)
	}
	if after.Arrange.Placements[0].AtTick != p.AtTick || after.Arrange.Placements[0].LengthTicks != p.LengthTicks {
		t.Fatal("format changed ticks")
	}
	for _, v := range []string{"1/7", "@0.1.1", "@1.5.1", "9007199254740992ticks"} {
		if _, err := MusicalTicks(v, false); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}

func TestSetPlacementFieldRefusals(t *testing.T) {
	source := []byte(arrangeFixture)
	for _, c := range []struct{ name, field, value, want string }{
		{"bass-1", "color", "1bar", `unknown placement field "color"`},
		{"missing", "at", "@1.1.1", `placement "missing" has no at field`},
		{"bass-1", "length", "@1.1.1", "expected exact ticks, bars, or note division"},
		{"bass-1", "at", "soon", "expected exact ticks, bars, or note division"},
	} {
		if got, err := SetPlacementField(source, c.name, c.field, c.value); err == nil || got != nil || !bytes.Contains([]byte(err.Error()), []byte(c.want)) {
			t.Errorf("%s.%s=%s: got %q, %v; want error containing %q", c.name, c.field, c.value, got, err, c.want)
		}
	}
	moved, err := SetPlacementField(source, "bass-1", "at", "@1.1.1")
	if err != nil || !bytes.Equal(moved, bytes.Replace(source, []byte("@2.3.4"), []byte("@1.1.1"), 1)) {
		t.Fatalf("moved %q, %v", moved, err)
	}
}
