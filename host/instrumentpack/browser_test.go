package instrumentpack

import (
	"os/exec"
	"testing"
)

func TestBrowserInstrumentPackLoader(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for browser loader tests")
	}
	if output, err := exec.Command(node, "--test", "../web/instrument-pack.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("browser loader regression: %v\n%s", err, output)
	}
}
