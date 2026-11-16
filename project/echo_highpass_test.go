package project

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// TestEchoHighpassValues checks the effect block's highpass on a delay and a
// tape: a frequency from 20 Hz to 2 kHz, or off; anything else is refused with
// a message that names the range.
func TestEchoHighpassValues(t *testing.T) {
	for _, kind := range []string{"delay", "tape"} {
		for value, want := range map[string]string{
			"300Hz":  "",
			"2kHz":   "",
			"20Hz":   "",
			"off":    "",
			"10Hz":   "20",
			"2.5kHz": "2000",
			"300":    "Hz",
			"40ms":   "Hz",
		} {
			source := "cicada 2\ntempo 120\nfx echo " + kind + " { highpass = " + value + " }\ntrack a acid { send echo = 0.5 }\npattern p { c3 }\nscene main { a = p }\nsong { main }\n"
			score, ds := notation.Parse([]byte(source))
			if !hasErrors(ds) {
				_, ds = FromScore(score)
			}
			if want == "" {
				if hasErrors(ds) {
					t.Errorf("%s highpass = %s: %v", kind, value, ds)
				}
				continue
			}
			if !hasErrors(ds) || !strings.Contains(ds[0].Message, want) {
				t.Errorf("%s highpass = %s: want an error naming %q, got %v", kind, value, want, ds)
			}
		}
	}
}

// TestEchoHighpassRoundTripsThroughJSON: a score with the control is
// formatter-clean, checks, exports to the JSON form inside both schemas'
// delay_effect, and comes back to the same JSON bytes.
func TestEchoHighpassRoundTripsThroughJSON(t *testing.T) {
	for _, value := range []string{"300Hz", "off"} {
		source := "cicada 2\ntempo 120\nfx echo delay { highpass = " + value + " }\ntrack a acid { send echo = 0.5 }\npattern p { c3 }\nscene main { a = p }\nsong { main }\n"
		format := func(text []byte) []byte {
			document, err := notation.ParseDocument(text)
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := notation.Format(document)
			if err != nil {
				t.Fatal(err)
			}
			return formatted
		}
		formatted := format([]byte(source))
		if string(format(formatted)) != string(formatted) || !strings.Contains(string(formatted), "highpass = "+value) {
			t.Fatalf("highpass = %s does not survive formatting:\n%s", value, formatted)
		}
		score, ds := notation.Parse(formatted)
		if hasErrors(ds) {
			t.Fatal(ds)
		}
		p, ds := FromScore(score)
		if p == nil {
			t.Fatalf("highpass = %s: %v", value, ds)
		}
		encoded, err := CanonicalJSON(p)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Effects []struct {
				Params map[string]json.RawMessage `json:"params"`
			} `json:"effects"`
		}
		if err := json.Unmarshal(encoded, &doc); err != nil {
			t.Fatal(err)
		}
		exported := string(doc.Effects[0].Params["highpass"])
		for _, schemaPath := range []string{"schema/project-1.json", "schema/project-2.json"} {
			data, err := os.ReadFile(schemaPath)
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Defs map[string]struct {
					Properties struct {
						Params struct {
							Properties map[string]json.RawMessage `json:"properties"`
						} `json:"params"`
					} `json:"properties"`
				} `json:"$defs"`
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			form := string(schema.Defs["delay_effect"].Properties.Params.Properties["highpass"])
			want := `"const": "hz"`
			if value == "off" {
				want = `"const": "off"`
			}
			if form == "" || !strings.Contains(form, want) {
				t.Fatalf("%s delay_effect has no highpass form for %s (exported %s)", schemaPath, value, exported)
			}
		}
		decoded, err := DecodeJSON(encoded)
		if err != nil {
			t.Fatal(err)
		}
		back, err := ToSource(decoded)
		if err != nil {
			t.Fatal(err)
		}
		again, ds := notation.Parse(back)
		if hasErrors(ds) {
			t.Fatalf("%s\n%v", back, ds)
		}
		p2, ds := FromScore(again)
		if p2 == nil {
			t.Fatal(ds)
		}
		reencoded, err := CanonicalJSON(p2)
		if err != nil {
			t.Fatal(err)
		}
		if string(reencoded) != string(encoded) {
			t.Fatalf("highpass = %s changed in the round trip:\n%s\n%s", value, encoded, reencoded)
		}
	}
}
