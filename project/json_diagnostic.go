package project

import (
	"bytes"
	"encoding/json"
	"errors"
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

func jsonError(data []byte, code, pointer string, offset int, cause error) error {
	var syntax *json.SyntaxError
	if errors.As(cause, &syntax) {
		offset = int(syntax.Offset)
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

func jsonRootFieldOffset(data []byte, field string) int {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return 0
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return 0
		}
		if key == field {
			return jsonKeyOffset(data, int(decoder.InputOffset()))
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0
		}
	}
	return 0
}
