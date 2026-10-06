package instrumentpack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSamplerReportsStayOutsideRepository(t *testing.T) {
	for _, report := range []string{
		"docs/sampler/results.md",
		"docs/sampler/evidence/wasm-timing.jsonl",
		"docs/sampler/evidence/browser.json",
		"docs/sampler/evidence/source-reference.json",
		"assets/sampler/full-kit/measurements.json",
		"docs/sampler/full-kit-results.md",
	} {
		if _, err := os.Stat(filepath.Join("..", "..", report)); !os.IsNotExist(err) {
			t.Errorf("report %s must be retained as a CI artifact or PR attachment: %v", report, err)
		}
	}
}
