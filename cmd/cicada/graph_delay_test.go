package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/instrument"
)

func TestGraphCommandJSON(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cicada")
	build := exec.Command("go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		file, name string
		period     bool
	}{
		{"glassbass.cicada", "glassbass", false},
		{"pluck.cicada", "plucked", true},
		{"pluck.cicada", "slapback", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command(bin, "graph", filepath.Join("..", "..", "examples", tc.file), tc.name)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatalf("graph: %v\n%s", err, &stderr)
			}
			var program instrument.Program
			if err := json.Unmarshal(output, &program); err != nil {
				t.Fatalf("invalid graph JSON: %v\n%s", err, output)
			}
			if program.Name != tc.name || program.Mode != "mono" || program.StatefulNodes == 0 || program.Output < 0 || program.Output >= len(program.Nodes) {
				t.Fatalf("incomplete graph: %+v", program)
			}
			if program.Nodes[program.Output].Type != instrument.Audio {
				t.Fatalf("wrong output node: %+v", program.Nodes[program.Output])
			}
			var hasPeriod bool
			for _, node := range program.Nodes {
				hasPeriod = hasPeriod || node.Op == "period"
			}
			if hasPeriod != tc.period || (program.DelaySamples > 0) != tc.period {
				t.Fatalf("period=%t, delay samples=%d; want period and delay=%t", hasPeriod, program.DelaySamples, tc.period)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(output, &fields); err != nil {
				t.Fatal(err)
			}
			if _, present := fields["PeriodExpressions"]; present {
				t.Fatal("compiler expression metadata leaked into graph JSON")
			}
		})
	}
}

func TestCheckGraphDelayDiagnostics(t *testing.T) {
	for _, tc := range []struct{ expression, params, note, code string }{
		{"delay(noise(), 440Hz)", "", "a3", "CICADA-UNIT"},
		{"delay(noise(), 100ms)", "", "a3", "CICADA-PARAM"},
		{"delay(noise(), time)", "time = 100ms", "a3", "CICADA-PARAM"},
		{"comb(noise(), 2 / pitch, 0.99, 0.5)", "", "c0", "CICADA-PARAM"},
		{"delay(delay(delay(noise(), 1ms), 2ms), 3ms)", "", "a3", "CICADA-LIMIT"},
	} {
		path := filepath.Join(t.TempDir(), "invalid.cicada")
		source := fmt.Sprintf("instrument sound { param time: ms = 10ms voice mono { out = %s } } track t sound { %s } pattern p notes { %s } scene s { t=p } song { s }", tc.expression, tc.params, tc.note)
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if err := checkCommand([]string{path}, &stdout, &stderr); err == nil {
			t.Fatalf("invalid delay accepted: %s %s %s", tc.expression, tc.params, tc.note)
		}
		if !bytes.Contains(stderr.Bytes(), []byte(tc.code)) {
			t.Fatalf("missing %s: %s", tc.code, &stderr)
		}
	}
}
