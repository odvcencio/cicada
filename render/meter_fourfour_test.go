package render

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const plainFourFour = `cicada 2
tempo 120
track bass acid {}
pattern riff { c2 . g2 . c3 . eb3 . }
scene a { bass = riff }
song { a*2 }
`

// A plain `meter 4/4` is the same music as no declaration, so it gives the same
// semantic JSON, kernel image and WAV bytes on both render paths: one encoding
// for one music (#358 review). A 4/4 with written beat groups is kept, because
// the groups reach the MIDI time signature.
func TestPlainFourFourIsNoDeclaration(t *testing.T) {
	declared := strings.Replace(plainFourFour, "tempo 120\n", "tempo 120\nmeter 4/4\n", 1)
	files := func(source string) (json, image []byte, wav [2][]byte) {
		t.Helper()
		score, ds := notation.Parse([]byte(source))
		if score == nil || hasRenderErrors(ds) {
			t.Fatal(ds)
		}
		p, ds := project.FromScore(score)
		if p == nil || hasRenderErrors(ds) {
			t.Fatal(ds)
		}
		json, err := project.CanonicalJSON(p)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := project.CompileEngine(p, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		if image, err = kernelimage.Encode(cfg); err != nil {
			t.Fatal(err)
		}
		for i, scheduled := range []bool{false, true} {
			var out bytes.Buffer
			if _, _, err := pathsRender(score, scheduled, "", 2, 44100, &out); err != nil {
				t.Fatal(err)
			}
			wav[i] = out.Bytes()
		}
		return json, image, wav
	}
	j1, i1, w1 := files(plainFourFour)
	j2, i2, w2 := files(declared)
	if !bytes.Equal(j1, j2) || !bytes.Equal(i1, i2) || !bytes.Equal(w1[0], w2[0]) || !bytes.Equal(w1[1], w2[1]) {
		t.Fatalf("meter 4/4 differs from no declaration: JSON %v, image %v, WAV %v %v", bytes.Equal(j1, j2), bytes.Equal(i1, i2), bytes.Equal(w1[0], w2[0]), bytes.Equal(w1[1], w2[1]))
	}
	grouped := strings.Replace(plainFourFour, "tempo 120\n", "tempo 120\nmeter 4/4 2+2\n", 1)
	if j3, _, _ := files(grouped); bytes.Equal(j1, j3) || !bytes.Contains(j3, []byte(`"groups"`)) {
		t.Fatal("meter 4/4 2+2 lost its beat groups")
	}
}

func hasRenderErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
