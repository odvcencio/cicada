package main

import (
	"bytes"
	"encoding/json"
	"io"
)

// marshalAgentJSON indents a --json document and leaves <, > and & as they
// are, so text such as pattern:<name> reads as written.
func marshalAgentJSON(v any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// writeAgentJSON writes one --json document and a newline.
func writeAgentJSON(w io.Writer, v any) error {
	data, err := marshalAgentJSON(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
