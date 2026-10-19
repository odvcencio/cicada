package notation

import (
	"bytes"
	"reflect"
	"testing"
)

func TestFormatPerformanceKeepsRequiredTerminatorsAndSharedBindings(t *testing.T) {
	source := []byte(`cicada 2
track keys acid {}
pattern phrase performance length=3840ticks {
 // one shared breath
 gesture-group touch { gesture at=0ticks bend=0ct pressure=0 timbre=0.5; gesture at=960ticks bend=100ct pressure=0.8 timbre=0.9; }
 note 60 at=17ticks length=703ticks velocity=103 group=touch { gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7; }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch; // held independently
}
scene main { keys=phrase }
song { main }
`)
	doc, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	before, ds := Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	after, ds := Parse(formatted)
	if len(ds) != 0 || after == nil {
		t.Fatalf("formatter invalidated performed source: %v\n%s", ds, formatted)
	}
	if !reflect.DeepEqual(before.Patterns[0].Performance, after.Patterns[0].Performance) {
		t.Fatal("formatter changed member or shared expressions")
	}
	if !bytes.Contains(formatted, []byte("// one shared breath")) || !bytes.Contains(formatted, []byte("// held independently")) || !bytes.Contains(formatted, []byte("note 64 at = 19ticks length = 1301ticks velocity = 87 group = touch;")) {
		t.Fatalf("format lost comments or split note attributes: %s", formatted)
	}
	doc, err = ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format(doc)
	if err != nil || !bytes.Equal(formatted, again) {
		t.Fatal("performance formatting is not idempotent", err)
	}
}
