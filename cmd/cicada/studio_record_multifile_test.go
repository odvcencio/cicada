package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
)

func TestRecordRouteRefusesForeignExpressionPositions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(map[string]string{"\n": "lf", "\r\n": "crlf"}[newline], func(t *testing.T) {
			dir := t.TempDir()
			entry := []byte("cicada 2\n" + strings.Repeat("// unrelated entry text "+strings.Repeat("x", 100)+"\n", 12))
			part := []byte("track lead acid {}\npattern melody acid steps=4 { . . . .\n  bend: 0ct 0ct 0ct 0ct\n  vibrato: 0ct 0ct 0ct 0ct\n  pressure: 0 0 0 0\n  timbre: 0.5 0.5 0.5 0.5\n}\nscene main { lead=melody }\nsong { main }\n")
			entry = bytes.ReplaceAll(entry, []byte("\n"), []byte(newline))
			part = bytes.ReplaceAll(part, []byte("\n"), []byte(newline))
			for name, source := range map[string][]byte{"main.cicada": entry, "part.cicada": part, "cicada.mod": []byte("project recording\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n")} {
				if err := os.WriteFile(filepath.Join(dir, name), source, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := newStudio(filepath.Join(dir, "main.cicada"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.shutdown()
			before := noopFiles(t, dir, os.Getenv("XDG_CONFIG_HOME"))
			response := studioCall(t, s.routes(), "/api/record", studioEdit{Revision: studioRevision(entry), Track: "lead", Pattern: "melody", Take: []studioTakeNote{{Note: 60, Velocity: 90, EndTick: 120, Expressions: []studioTakeExpression{{PitchCents: 50, Timbre: .5}}}}})
			var reply map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "another source file") {
				t.Fatalf("foreign expression must be refused: %d %s", response.Code, response.Body.String())
			}
			if got := noopFiles(t, dir, os.Getenv("XDG_CONFIG_HOME")); !reflect.DeepEqual(before, got) {
				t.Fatal("refused recording changed a project or recovery file")
			}
			if len(s.history.historySnapshot().Edits) != 0 || s.history.pendingTakeRevision != "" {
				t.Fatal("refused recording changed history or the playback handoff")
			}
			if records, _, err := editlog.ReadLog(editlog.Path(s.path)); err != nil || len(records) != 0 {
				t.Fatalf("refused recording logged an edit: %+v %v", records, err)
			}
		})
	}
}
