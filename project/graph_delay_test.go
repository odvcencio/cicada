package project

import (
	"bytes"
	"fmt"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestGraphDelayPeriodRoundtrip(t *testing.T) {
	for _, expression := range []string{"1 / pitch", "1 / 440Hz", "2 / (pitch * 2)", "10ms / 2"} {
		source := fmt.Sprintf("instrument sound { voice mono { let time = %s out = comb(noise(), time, 0.99, 0.5) } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", expression)
		score, ds := notation.Parse([]byte(source))
		if score == nil || len(ds) != 0 {
			t.Fatalf("parse: %+v", ds)
		}
		p, ds := FromScore(score)
		if p == nil {
			t.Fatalf("project %s: %+v", expression, ds)
		}
		data, err := CanonicalJSON(p)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		text, err := ToSource(decoded)
		if err != nil {
			t.Fatal(err)
		}
		score, ds = notation.Parse(text)
		if score == nil || len(ds) != 0 {
			t.Fatalf("parse roundtrip: %+v", ds)
		}
		p, ds = FromScore(score)
		if p == nil {
			t.Fatalf("roundtrip: %+v\n%s", ds, text)
		}
		again, err := CanonicalJSON(p)
		if err != nil || !bytes.Equal(data, again) {
			t.Fatalf("period changed in roundtrip %s: %v\n%s", expression, err, text)
		}
	}
}
