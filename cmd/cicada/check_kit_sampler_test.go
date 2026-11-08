package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testpack"
)

func checkKitScore(t *testing.T, options testpack.Options, kit, rows string) (error, string) {
	t.Helper()
	return checkKitPattern(t, options, kit, "", rows)
}

// checkKitPattern is checkKitScore with extra pattern attributes, such as
// humanize = 32.
func checkKitPattern(t *testing.T, options testpack.Options, kit, attributes, rows string) (error, string) {
	t.Helper()
	dir := t.TempDir()
	pack := testpack.Write(t, dir, options)
	source := "cicada 2\ntempo 120\n" + pack.Sampler("shells", 4) + "kit rec {\n" + kit + "}\ntrack drums rec {}\n" +
		"pattern p drums steps=4 " + attributes + " {\n" + rows + "}\nscene main { drums = p }\nsong { main }\n"
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := checkPaths([]string{path}, &stdout, &stderr)
	return err, stderr.String()
}

func TestCheckPassesAKitWithinThePackZones(t *testing.T) {
	zones := testpack.Options{Zones: []testpack.Zone{{KeyLow: 37, KeyHigh: 38}}}
	if err, report := checkKitScore(t, zones, "  sd = shells.d2\n  rs = shells.c#2\n", "  sd: x... rs: ..x.\n"); err != nil {
		t.Fatalf("%v\n%s", err, report)
	}
}

func TestCheckReportsAKitNoteOutsideThePackZones(t *testing.T) {
	zones := testpack.Options{Zones: []testpack.Zone{{KeyLow: 37, KeyHigh: 38}}}
	err, report := checkKitScore(t, zones, "  sd = shells.d2\n  cp = shells.e2\n", "  sd: x...\n")
	if err == nil || !strings.Contains(report, "CICADA-SAMPLER-ZONE") || !strings.Contains(report, "lane cp binds shells.e2 (MIDI 40)") {
		t.Fatalf("want a zone error for the cp lane: %v\n%s", err, report)
	}
}

// A kit lane plays a hit above the note's loudest recorded layer at that
// layer, so check warns rather than fails.
func TestCheckWarnsAKitHitAboveTheRecordedVelocities(t *testing.T) {
	quiet := testpack.Options{Zones: []testpack.Zone{{KeyLow: 38, KeyHigh: 38, VelocityHigh: 40}}}
	err, report := checkKitScore(t, quiet, "  sd = shells.d2\n", "  sd: x9...\n")
	if err != nil || !strings.Contains(report, "warning CICADA-SAMPLER-ZONE") || !strings.Contains(report, "records that note only at velocities 1 to 40, so it plays at 40") {
		t.Fatalf("want a velocity clamp warning: %v\n%s", err, report)
	}
}

// A velocity inside the recorded range that no zone covers faults the kit,
// including one that only humanize reaches.
func TestCheckReportsAKitVelocityGapThatHumanizeReaches(t *testing.T) {
	gapped := testpack.Options{Zones: []testpack.Zone{{KeyLow: 38, KeyHigh: 38, VelocityLow: 1, VelocityHigh: 40}, {KeyLow: 38, KeyHigh: 38, VelocityLow: 80, VelocityHigh: 127}}}
	if err, report := checkKitPattern(t, gapped, "  sd = shells.d2\n", "", "  sd: x...\n"); err != nil {
		t.Fatalf("velocity 100 is recorded: %v\n%s", err, report)
	}
	err, report := checkKitPattern(t, gapped, "  sd = shells.d2\n", "humanize = 32", "  sd: x...\n")
	if err == nil || !strings.Contains(report, "error CICADA-SAMPLER-ZONE") || !strings.Contains(report, "covers velocity 68") {
		t.Fatalf("want a humanize gap error: %v\n%s", err, report)
	}
}

func TestCheckReportsAnUnknownKitSampler(t *testing.T) {
	err, report := checkKitScore(t, testpack.Options{}, "  sd = drumz.d2\n", "  sd: x...\n")
	if err == nil || !strings.Contains(report, "no sampler named drumz") {
		t.Fatalf("%v\n%s", err, report)
	}
}
