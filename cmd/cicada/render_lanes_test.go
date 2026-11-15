package main

import (
	"errors"
	"io"
	"testing"

	"m31labs.dev/cicada/internal/renderlanes"
	"m31labs.dev/cicada/render"
)

func TestParseJobs(t *testing.T) {
	for _, c := range []struct {
		text string
		want int
		ok   bool
	}{
		{"", 0, true}, // absent: the serial render
		{"auto", render.AutoJobs, true},
		{"1", 1, true},
		{"4", 4, true},
		{"99", 99, true}, // capped later, at the CPUs
		{"0", 0, false},
		{"-1", 0, false},
		{"x", 0, false},
		{"Auto", 0, false},
	} {
		got, err := parseJobs(c.text)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("parseJobs(%q) = %d, %v; want %d, ok %t", c.text, got, err, c.want, c.ok)
		}
		var jobsErr *jobsArgError
		if !c.ok && !errors.As(err, &jobsErr) {
			t.Errorf("parseJobs(%q) error %v is not a *jobsArgError", c.text, err)
		}
	}
	// The render commands report the bad value instead of the usage line.
	_, _, _, err := parseRenderArgs([]string{"-o", "out.wav", "--jobs", "0"}, 24, io.Discard)
	var jobsErr *jobsArgError
	if !errors.As(err, &jobsErr) || jobsErr.Error() != `--jobs takes auto or a count of at least 1, not "0"` {
		t.Fatalf("--jobs 0: %v", err)
	}
}

// render.AutoJobs passes straight through to renderlanes.Jobs, so the two
// constants must stay equal.
func TestAutoJobsMatchesRenderLanes(t *testing.T) {
	if render.AutoJobs != renderlanes.AutoJobs {
		t.Fatalf("render.AutoJobs %d, renderlanes.AutoJobs %d", render.AutoJobs, renderlanes.AutoJobs)
	}
}
