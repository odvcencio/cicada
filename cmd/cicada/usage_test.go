package main

import (
	"os/exec"
	"strings"
	"testing"
)

// A command given the wrong arguments prints its own usage line, as cicada help
// shows it, not the usage of every command.
func TestMisusedCommandPrintsItsOwnUsage(t *testing.T) {
	bin := buildCicadaForTest(t)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"graph", "x.cicada"}, "usage: cicada graph <score.cicada> <instrument>"},
		{[]string{"events", "x.cicada"}, "usage: cicada events <score.cicada> <track> <pattern>"},
		{[]string{"validate", "a.cicada", "b.cicada"}, "usage: cicada validate"},
	} {
		output, err := exec.Command(bin, test.args...).CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 {
			t.Fatalf("%v: want exit status 2, got %v", test.args, err)
		}
		text := string(output)
		if !strings.HasPrefix(text, test.want) || strings.Contains(text, "| check [--profile") {
			t.Fatalf("%v: want only %q, got:\n%s", test.args, test.want, text)
		}
	}
}
