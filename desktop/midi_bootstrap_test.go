package main

import (
	"os/exec"
	"testing"
)

func TestNativeMIDIBootstrap(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed to exercise the native MIDI browser contract")
	}
	command := exec.Command(node, "--test", "testdata/midi-bootstrap.test.cjs")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native MIDI browser contract: %v\n%s", err, output)
	}
}
