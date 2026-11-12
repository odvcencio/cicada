package render

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

const echoHighpassScore = `cicada 2
tempo 120
seed 3
fx echo delay { time = 1/8 feedback = 0.8 damp = 12000Hz HIGHPASS }
track bass acid { cutoff = 900Hz send echo = 0.8 }
pattern line { c2 . . . g1 . . . c2 . . . . . . . }
SCENES
`

func echoHighpassRender(t *testing.T, highpass, scenes string) []byte {
	t.Helper()
	if scenes == "" {
		scenes = "scene main { bass = line }\nsong { main*4 }"
	}
	source := strings.NewReplacer("HIGHPASS", highpass, "SCENES", scenes).Replace(echoHighpassScore)
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatalf("%s", d.Error())
		}
	}
	var wav bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48_000, Bits: 24, TailSec: 1}, &wav); err != nil {
		t.Fatalf("highpass %q, scenes %q: %v", highpass, scenes, err)
	}
	return wav.Bytes()
}

// TestEchoHighpassInAScore renders the delay return with the in-loop
// high-pass: highpass = off renders the same bytes as a score without it; a
// cutoff changes the repeats; scenes and automate move it on the direct
// renderer and, in an arrangement, on the scheduled engine; and a tape echo
// takes it too.
func TestEchoHighpassInAScore(t *testing.T) {
	plain := echoHighpassRender(t, "", "")
	if !bytes.Equal(plain, echoHighpassRender(t, "highpass = off", "")) {
		t.Fatal("highpass = off changed the render")
	}
	thin := echoHighpassRender(t, "highpass = 400Hz", "")
	if bytes.Equal(plain, thin) {
		t.Fatal("highpass = 400Hz did not change the repeats")
	}
	for name, scenes := range map[string]string{
		"scenes":      "scene main { bass = line echo.highpass = 600Hz }\nscene dry { bass = line echo.highpass = off }\nsong { main*2 dry*2 }",
		"automate":    "scene main { bass = line }\nsong { main*4 }\nautomate echo.highpass { @1.1.1 60Hz @3.1.1 1800Hz exponential @4.1.1 200Hz }",
		"arrangement": "arrange { place a bass line { at = @1.1.1 length = 4bars } }\nautomate echo.highpass { @1.1.1 60Hz @3.1.1 1800Hz exponential }",
	} {
		moved := echoHighpassRender(t, "", scenes)
		if bytes.Equal(plain, moved) {
			t.Errorf("%s: moving echo.highpass changed nothing", name)
		}
	}
	tape := strings.Replace(echoHighpassScore, "fx echo delay { time = 1/8 feedback = 0.8 damp = 12000Hz HIGHPASS }", "fx echo tape { time = 1/8 feedback = 0.8 damp = 12000Hz highpass = 300Hz }", 1)
	tape = strings.Replace(tape, "SCENES", "scene main { bass = line }\nsong { main*4 }\nautomate echo.highpass { @1.1.1 100Hz @3.1.1 900Hz }", 1)
	score, ds := notation.Parse([]byte(tape))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatalf("tape: %s", d.Error())
		}
	}
	if _, err := WAV(score, Options{SampleRate: 48_000, Bits: 24, TailSec: 1}, &bytes.Buffer{}); err != nil {
		t.Fatalf("tape with highpass: %v", err)
	}
}
