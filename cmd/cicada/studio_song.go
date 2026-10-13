package main

import (
	"fmt"
)

func replaceSongSpan(source []byte, start, end int, text []byte) ([]byte, error) {
	if start < 0 || end < start || end > len(source) {
		return nil, fmt.Errorf("song source span is invalid")
	}
	updated := make([]byte, 0, len(source)-end+start+len(text))
	updated = append(updated, source[:start]...)
	updated = append(updated, text...)
	updated = append(updated, source[end:]...)
	return updated, nil
}
