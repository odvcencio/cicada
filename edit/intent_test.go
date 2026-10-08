package edit

import (
	"strings"
	"testing"
)

type probe struct {
	Name string `json:"name"`
}

func (probe) Kind() string { return "probe" }

// One registration for every test in the package; the key equals Kind().
func init() { Register("probe", func() Intent { return &probe{} }) }

func TestIntentRoundTripByKind(t *testing.T) {
	raw, err := encodeIntent(&probe{Name: "x"})
	if err != nil || string(raw) != `{"kind":"probe","name":"x"}` {
		t.Fatalf("encode: %s %v", raw, err)
	}
	decoded, err := decodeIntent(raw)
	if err != nil || decoded.(*probe).Name != "x" {
		t.Fatalf("decode: %+v %v", decoded, err)
	}
	if _, err := decodeIntent([]byte(`{"kind":"nope"}`)); err == nil || !strings.Contains(err.Error(), `unknown intent kind "nope"`) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := decodeIntent([]byte(`{"kind":"probe","extra":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestRegistryKeyEqualsKind(t *testing.T) {
	for _, kind := range Kinds() {
		if got := registry[kind]().Kind(); got != kind {
			t.Errorf("registered %q but Kind() returns %q", kind, got)
		}
	}
}
