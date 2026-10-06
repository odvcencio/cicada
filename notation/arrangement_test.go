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
