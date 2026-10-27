package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The expected offsets are the byte that cannot continue a JSON text, or the
// length of the input when it ends early. They hold on every Go version; the
// standard library's own syntax-error offsets do not.
func TestJSONSyntaxOffset(t *testing.T) {
	for _, test := range []struct {
		name, input string
		offset      int
	}{
		{"empty", "", 0},
		{"only space", " \n\t", 3},
		{"bad first byte", "!", 0},
		{"byte order mark", "\xef\xbb\xbf{}", 0},
		{"open object", "{", 1},
		{"open array", "[1,", 3},
		{"key without colon", `{"a"`, 4},
		{"wrong key", `{1:2}`, 1},
		{"missing colon", `{"a" 1}`, 5},
		{"missing value", `{"a":}`, 5},
		{"missing value at end", `{"a":`, 5},
		{"missing comma", `{"a":1 "b":2}`, 7},
		{"object trailing comma", `{"a":1,}`, 7},
		{"array trailing comma", `[1,]`, 3},
		{"leading comma", `[,1]`, 1},
		{"stray closer", `]`, 0},
		{"extra value", `{}{`, 2},
		{"extra byte", "[1]x", 3},
		{"truncated true", `tru`, 3},
		{"wrong literal", `trux`, 3},
		{"truncated null", `[nul`, 4},
		{"lone minus", `-`, 1},
		{"minus then letter", `-x`, 1},
		{"leading zero", `[01]`, 2},
		{"bare plus", `+1`, 0},
		{"bare fraction", `.5`, 0},
		{"fraction without digits", `[1.]`, 3},
		{"fraction then letter", `1.x`, 2},
		{"exponent without digits", `1e`, 2},
		{"signed exponent without digits", `1e+`, 3},
		{"unterminated string", `"abc`, 4},
		{"unterminated key", `{"ab`, 4},
		{"control byte in string", "\"a\nb\"", 2},
		{"nul byte in string", "[\"a\x00\"]", 3},
		{"bad escape", `"a\x"`, 3},
		{"escape at end", `"a\`, 3},
		{"bad unicode escape digit", `"\u12g4"`, 5},
		{"short unicode escape", `"\u12"`, 5},
		{"unicode escape at end", `"\u12`, 5},
		{"after multibyte text", "[\"é🎵\", !]", 11},
		{"newline then bad byte", "{\n  \"x\": }", 9},
	} {
		t.Run(test.name, func(t *testing.T) {
			offset, found := jsonSyntaxOffset([]byte(test.input))
			if !found || offset != test.offset {
				t.Fatalf("jsonSyntaxOffset(%q) = %d, %v; want %d, true", test.input, offset, found, test.offset)
			}
			if json.Valid([]byte(test.input)) {
				t.Fatalf("%q is valid JSON", test.input)
			}
		})
	}
}

func TestJSONSyntaxOffsetAcceptsValidText(t *testing.T) {
	for _, input := range []string{
		`{}`, `[]`, `null`, `true`, `false`, `0`, `-0`, `10`, `-1.5e+3`, `2E-2`, `0.5`,
		`""`, `"a\"b\\c\/d\b\f\n\r\té😀"`, "\"é🎵\"",
		" \t\r\n[1, 2.5e3, \"x\", null, true, false, {\"a\": [], \"b\": {}}]\n",
		strings.Repeat("[", 500) + strings.Repeat("]", 500),
	} {
		if offset, found := jsonSyntaxOffset([]byte(input)); found {
			t.Fatalf("jsonSyntaxOffset(%.40q) = %d, true; want no error", input, offset)
		}
	}
}

func TestJSONSyntaxOffsetStopsAtExcessiveDepth(t *testing.T) {
	deep := strings.Repeat("[", jsonScanDepth+2)
	if offset, found := jsonSyntaxOffset([]byte(deep)); found {
		t.Fatalf("scanner reported %d for input nested beyond its depth limit", offset)
	}
}

// The scanner decides only where an error is, never whether the text is valid.
// Every truncation, deletion, insertion and replacement of a few documents must
// agree with the standard library on validity.
func TestJSONSyntaxOffsetAgreesWithStandardLibraryValidity(t *testing.T) {
	seeds := []string{
		`{"a":[1,2,{"b":"c"}],"d":true,"e":null,"f":"xé\n"}`,
		"{\n  \"title\": \"é🎵\",\n  \"x\": [1.5, -2e3, 0]\n}\n",
		`[[[],[1],{"k":[{"z":-1.5e3}]}],false,"\"\\\/\b\f\n\r\t"]`,
	}
	inserts := []string{"!", "}", "]", "{", "[", ",", ":", `"`, `\`, " ", "\n", "x", "0", "-", ".", "e", "+", "t", "n", "\x01", "u"}
	count := 0
	check := func(label string, data []byte) {
		count++
		offset, found := jsonSyntaxOffset(data)
		if found == json.Valid(data) {
			t.Fatalf("%s: %q: scanner found an error = %v, json.Valid = %v", label, data, found, json.Valid(data))
		}
		if found && (offset < 0 || offset > len(data)) {
			t.Fatalf("%s: %q: offset %d outside the input", label, data, offset)
		}
		if found && offset < len(data) && json.Valid(data[:offset+1]) {
			t.Fatalf("%s: %q: text through the reported byte %d is valid JSON", label, data, offset)
		}
	}
	for si, seed := range seeds {
		for i := 0; i <= len(seed); i++ {
			if !validUTF8Prefix(seed, i) {
				continue
			}
			check(fmt.Sprintf("seed %d truncate %d", si, i), []byte(seed[:i]))
			for _, ins := range inserts {
				check(fmt.Sprintf("seed %d insert %q at %d", si, ins, i), []byte(seed[:i]+ins+seed[i:]))
				if i < len(seed) && validUTF8Prefix(seed, i+1) {
					check(fmt.Sprintf("seed %d replace %d with %q", si, i, ins), []byte(seed[:i]+ins+seed[i+1:]))
				}
			}
			if i < len(seed) && validUTF8Prefix(seed, i+1) {
				check(fmt.Sprintf("seed %d delete %d", si, i), []byte(seed[:i]+seed[i+1:]))
			}
		}
	}
	if count < 5000 {
		t.Fatalf("only %d mutations checked", count)
	}
}

// validUTF8Prefix reports whether seed[:n] ends on a character boundary, so
// that mutations never split a multibyte character; DecodeJSON rejects invalid
// UTF-8 before it scans syntax.
func validUTF8Prefix(seed string, n int) bool {
	return n == len(seed) || seed[n]&0xC0 != 0x80
}

// Truncated documents end at the end of the input on every Go version. Only the
// line and column are checked: the JSON Pointer of an unfinished value depends
// on how the standard library's decoder reports its end.
func TestJSONTruncatedInputLocations(t *testing.T) {
	for _, test := range []struct {
		name, input string
		line, col   int
	}{
		{"empty", "", 1, 1},
		{"open array", `{"x":[1,2`, 1, 10},
		{"unterminated string", `{"x":"abc`, 1, 10},
		{"unterminated literal", `{"x":tru`, 1, 9},
		{"trailing newline", "{\"x\":\n", 2, 1},
		{"after multibyte text", "{\"x\":\"é🎵", 1, 9},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeJSON([]byte(test.input))
			var diagnostic *JSONError
			if !errors.As(err, &diagnostic) || diagnostic.Code != "CICADA-SYNTAX" {
				t.Fatalf("diagnostic = %v, want CICADA-SYNTAX", err)
			}
			if diagnostic.Line != test.line || diagnostic.Column != test.col {
				t.Fatalf("diagnostic at %d:%d (%v), want %d:%d", diagnostic.Line, diagnostic.Column, diagnostic.Cause, test.line, test.col)
			}
		})
	}
}
