package main

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestViewAutomationUsesMusicalPointsAndAccessibleCurves(t *testing.T) {
	p, err := loadProject("../../examples/continuous-automation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	level := -6.0
	p.Automation = append(p.Automation, project.AutomationLane{Path: "bass.level", Points: []project.AutomationPoint{{Tick: 0, Value: project.SceneValue{Number: &level, Unit: "db"}, Shape: "linear"}}})
	views := automationViews(p, p.SongEndTick())
	found := false
	for _, v := range views {
		if v.Path == "bass.level" {
			found = true
			if v.Low != "-6" || v.High != "-6" || v.Points[0].Y != 64 {
				t.Fatalf("level view: %+v", v)
			}
		}
	}
	if !found {
		t.Fatal("missing level lane")
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
