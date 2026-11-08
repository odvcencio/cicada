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
	dir := t.TempDir()
	pack := testpack.Write(t, dir, options)
	source := "cicada 2\ntempo 120\n" + pack.Sampler("shells", 4) + "kit rec {\n" + kit + "}\ntrack drums rec {}\n" +
		"pattern p drums steps=4 {\n" + rows + "}\nscene main { drums = p }\nsong { main }\n"
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

func TestCheckReportsAKitHitVelocityNoZoneCovers(t *testing.T) {
	quiet := testpack.Options{Zones: []testpack.Zone{{KeyLow: 38, KeyHigh: 38, VelocityHigh: 40}}}
	err, report := checkKitScore(t, quiet, "  sd = shells.d2\n", "  sd: x9...\n")
	if err == nil || !strings.Contains(report, "CICADA-SAMPLER-ZONE") || !strings.Contains(report, "no zone of the pack for sampler shells covers that velocity") {
		t.Fatalf("want a velocity zone error: %v\n%s", err, report)
	}
}

func TestCheckReportsAnUnknownKitSampler(t *testing.T) {
	err, report := checkKitScore(t, testpack.Options{}, "  sd = drumz.d2\n", "  sd: x...\n")
	if err == nil || !strings.Contains(report, "no sampler named drumz") {
		t.Fatalf("%v\n%s", err, report)
	}
}
