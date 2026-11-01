package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

// JSONError carries a stable diagnostic code and JSON Pointer for interchange
// failures. Line and Column count Unicode scalar values from one.
type JSONError struct {
	Code    string
	Pointer string
	Line    int
	Column  int
	Cause   error
}

func (e *JSONError) Error() string { return e.Cause.Error() }
func (e *JSONError) Unwrap() error { return e.Cause }

type jsonFieldError struct {
	Pointer string
	Cause   error
}

func (e *jsonFieldError) Error() string { return e.Cause.Error() }
func (e *jsonFieldError) Unwrap() error { return e.Cause }

func jsonError(data []byte, code, pointer string, offset int, cause error) error {
	var field *jsonFieldError
	if pointer == "" && errors.As(cause, &field) {
		pointer = field.Pointer
	}
	var syntax *json.SyntaxError
	if errors.As(cause, &syntax) {
		// Decoder.Token's SyntaxError offsets changed in Go 1.27. Unmarshal
		// reports the same one-based offending-byte offset on both toolchains;
		// use its validation-only RawMessage path for stable source locations.
		var raw json.RawMessage
		var canonical *json.SyntaxError
		if err := json.Unmarshal(data, &raw); errors.As(err, &canonical) {
			offset = int(canonical.Offset) - 1
			if canonical.Error() == "unexpected end of JSON input" {
				offset = len(data)
			}
		} else {
			offset = int(syntax.Offset) - 1
		}
	} else if pointer != "" && offset == 0 {
		if located, ok := jsonPointerOffset(data, pointer); ok {
			offset = located
		}
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(data) {
		offset = len(data)
	}
	line, column := 1, 1
	for len(data) > 0 && offset > 0 {
		r, size := utf8.DecodeRune(data)
		if size > offset {
			break
		}
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
		data = data[size:]
		offset -= size
	}
	return &JSONError{Code: code, Pointer: pointer, Line: line, Column: column, Cause: cause}
}

func jsonPointer(base, token string) string {
	escaped := make([]byte, 0, len(token))
	for i := 0; i < len(token); i++ {
		switch token[i] {
		case '~':
			escaped = append(escaped, '~', '0')
		case '/':
			escaped = append(escaped, '~', '1')
		default:
			escaped = append(escaped, token[i])
		}
	}
	return base + "/" + string(escaped)
}

// InputOffset is just after a key token. Find its opening quote so diagnostics
// point to the duplicate spelling, including when the key contains escapes.
func jsonKeyOffset(data []byte, end int) int {
	if end > len(data) || end < 2 || data[end-1] != '"' {
		return end
	}
	for i := end - 2; i >= 0; i-- {
		if data[i] != '"' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && data[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return i
		}
	}
	return end
}

func jsonPointerOffset(data []byte, target string) (int, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func(string) (int, bool)
	walk = func(pointer string) (int, bool) {
		token, err := decoder.Token()
		if err != nil {
			return 0, false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return 0, false
		}
		switch delim {
		case '{':
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return 0, false
				}
				child := jsonPointer(pointer, key.(string))
				if child == target {
					return jsonKeyOffset(data, int(decoder.InputOffset())), true
				}
				if offset, found := walk(child); found {
					return offset, true
				}
			}
			decoder.Token()
		case '[':
			index := 0
			for decoder.More() {
				child := jsonPointer(pointer, strconv.Itoa(index))
				if offset, found := walk(child); found {
					return offset, true
				}
				index++
			}
			decoder.Token()
		}
		return 0, false
	}
	return walk("")
}
