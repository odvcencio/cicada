package main

import (
	"bytes"
	"flag"
	"strconv"
	"strings"
	"testing"
)

func TestCLIBarNumbersAreOneBased(t *testing.T) {
	for _, test := range []struct {
		bar     int
		want    int
		warning bool
	}{
		{bar: 1, want: 0},
		{bar: 2, want: 1},
		{bar: 0, want: 0, warning: true},
	} {
		flags := flag.NewFlagSet("render", flag.ContinueOnError)
		flags.Int("from", 1, "one-based start bar")
		if err := flags.Parse([]string{"--from", strconv.Itoa(test.bar)}); err != nil {
			t.Fatal(err)
		}
		var warning bytes.Buffer
		got, err := cliBarOffset(test.bar, flags, &warning)
		if err != nil {
			t.Fatalf("--from %d: %v", test.bar, err)
		}
		if got != test.want {
			t.Errorf("--from %d mapped to internal offset %d, want %d", test.bar, got, test.want)
		}
		if test.warning != strings.Contains(warning.String(), "deprecated") {
			t.Errorf("--from %d warning = %q", test.bar, warning.String())
		}
	}
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	flags.Int("from", 1, "one-based start bar")
	if err := flags.Parse([]string{"--from", "-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cliBarOffset(-1, flags, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted a bar number below 1")
	}
}
