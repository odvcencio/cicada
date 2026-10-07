package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAutomationViewsUseMusicalPointsAndAccessibleCurves(t *testing.T) {
	p, err := loadProject("../../examples/continuous-automation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	for _, studio := range []bool{false, true} {
		var output bytes.Buffer
		if err := writeScorePage(&output, p, "", "score.cicada", studio, ""); err != nil {
			t.Fatal(err)
		}
		page := output.String()
		for _, text := range []string{`aria-label="Continuous automation lanes"`, `aria-label="bass.cutoff automation"`, "@5.1.1: 2400Hz, exponential", "automation-curve", "Bar 5"} {
			if !strings.Contains(page, text) {
				t.Errorf("view missing %s", text)
			}
		}
		if strings.Contains(page, "NaN") {
			t.Fatal("invalid curve geometry")
		}
	}
}
