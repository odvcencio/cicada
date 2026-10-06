package audioencoding

import (
	"os/exec"
	"testing"
)

func TestBrowserAdmission(t *testing.T) {
	node, e := exec.LookPath("node")
	if e != nil {
		t.Skip("Node is needed for browser admission checks")
	}
	if b, e := exec.Command(node, "--test", "../web/audio-encoding.test.cjs").CombinedOutput(); e != nil {
		t.Fatalf("browser admission: %v\n%s", e, b)
	}
}
