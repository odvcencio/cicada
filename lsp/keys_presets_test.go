package lsp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestKeyboardPresetTargetAndControlCompletion(t *testing.T) {
	for _, test := range []struct{ source, have, absent string }{
		{"preset selected {\n instrument=builtin.fm", "builtin.fm_ep", "builtin.clav"},
		{"preset selected {\n instrument=builtin.tonewheel_organ\n sc", "scanner", "cutoff"},
		{"preset selected {\n instrument=builtin.fm_ep\n op3_r", "op3_ratio", "pickup"},
		{"preset selected {\n instrument=builtin.poly_keys\n vo", "voices", "op1_ratio"},
	} {
		source := []byte(test.source)
		items, ok := presetCompletion(nil, source, utf16Position(source, len(source)))
		data, _ := json.Marshal(items)
		if !ok || !bytes.Contains(data, []byte(`"label":"`+test.have+`"`)) || bytes.Contains(data, []byte(`"label":"`+test.absent+`"`)) {
			t.Fatalf("keyboard preset completion for %s: %s", source, data)
		}
	}
}
