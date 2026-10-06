package instrumentpack

import (
	"os/exec"
	"testing"
)

func TestPackBuilderCanonicalGzip(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python is required for pack builder tests")
	}
	if output, err := exec.Command(python, "../../tools/sampler/test_canonical_gzip.py").CombinedOutput(); err != nil {
		t.Fatalf("pack gzip regression: %v\n%s", err, output)
	}
}
