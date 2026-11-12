package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestMeterNoticeNamesTheSongMeter(t *testing.T) {
	if _, ok := meterNotice(nil); ok {
		t.Fatal("a song without a meter got a notice")
	}
	if _, ok := meterNotice(&project.Meter{Numerator: 4, Denominator: 4}); ok {
		t.Fatal("a 4/4 song got a notice")
	}
	message, ok := meterNotice(&project.Meter{Numerator: 6, Denominator: 8, Groups: []int{3, 3}})
	if !ok || !strings.Contains(message, "in 6/8") || !strings.Contains(message, "in 4/4") {
		t.Fatalf("notice: %q", message)
	}
}
