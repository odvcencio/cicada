package lsp

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/project"
)

// The echo highpass is off by default and the score refuses 0 Hz, so hover
// and completion say "off", not "0Hz".
func TestEchoHighpassDefaultReadsOff(t *testing.T) {
	descriptor, ok := paramdefs.Lookup("fx.delay.highpass")
	if !ok {
		t.Fatal("no fx.delay.highpass row")
	}
	hover := resolvedParameterHover("echo.highpass", project.ResolvedParam{Path: "echo.highpass", Descriptor: descriptor})
	if !strings.Contains(hover, "Default: off") || strings.Contains(hover, "Default: 0Hz") {
		t.Fatalf("hover: %s", hover)
	}
}
