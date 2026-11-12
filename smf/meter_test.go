package smf

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// The time signature event writes N, log2 D, the clocks per click of the first
// beat group, and 8. A first group whose clicks exceed one byte (12/4 with a
// group of 12 would be 288) clicks every beat instead (#358 review).
func TestTimeSignatureClicks(t *testing.T) {
	for _, c := range []struct {
		meter string
		want  []byte
	}{
		{"", []byte{4, 2, 24, 8}},
		{"meter 7/8 2+2+3\n", []byte{7, 3, 24, 8}},
		{"meter 6/8 3+3\n", []byte{6, 3, 36, 8}},
		{"meter 12/4 12\n", []byte{12, 2, 24, 8}},
		{"meter 6/2 6\n", []byte{6, 1, 48, 8}},
	} {
		source := "cicada 2\n" + c.meter + "track bass acid {}\npattern p { c2 }\nscene a { bass = p }\nsong { a }\n"
		score, ds := notation.Parse([]byte(source))
		if score == nil {
			t.Fatal(ds)
		}
		p, ds := project.FromScore(score)
		if p == nil {
			t.Fatalf("%q: %v", c.meter, ds)
		}
		file, err := FromProject(p, 1)
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		for _, meta := range file.Tracks[0].Meta {
			if meta.Type == 0x58 {
				got = meta.Data
			}
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("%q: time signature % x, want % x", c.meter, got, c.want)
		}
	}
}
