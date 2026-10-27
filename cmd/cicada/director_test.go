package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/director"
)

// directorScore is the Game Director example score: two states, one macro with
// a layer, one stinger and two transitions.
const directorScore = `cicada 2
title "Game director"
tempo 120
key a minor
seed 4242
track bed acid { cutoff = 500Hz }
track battle acid { cutoff = 1400Hz }
track cue acid { cutoff = 2400Hz }
live {
  land = bar
  phrase = 8bars
  macro intensity = 0.3 smooth 400ms
  layers intensity { battle >= 0.5 attack 1bar release 3bars }
  state explore = calm
  state combat = fight
  stinger pickup = cue.hit quantize beat crossfade 10ms
  transition explore -> combat quantize bar crossfade 200ms
  transition combat -> explore quantize phrase crossfade 400ms
}
pattern bed-loop { 1 . 5 . | 3 . 5 . }
pattern battle-loop { 1^ 5 1' 5 | 3^ 5 7 5 }
pattern hit { 1'^ . . . }
scene calm { bed = bed-loop battle = off cue = off }
scene fight { bed = off battle = battle-loop cue = off }
song { calm*64 }
`

func writeDirectorScore(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "game.cicada")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// directorSurface reads a manifest the way a host does: strictly, and then
// through the Go SDK's own validation.
func directorSurface(t *testing.T, path string) director.Surface {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var surface director.Surface
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&surface); err != nil {
		t.Fatalf("manifest does not decode: %v", err)
	}
	if err := surface.Validate(); err != nil {
		t.Fatalf("manifest is invalid: %v", err)
	}
	return surface
}

func TestDirectorCommandWritesTheManifestHostsRead(t *testing.T) {
	score := writeDirectorScore(t, directorScore)
	output := filepath.Join(t.TempDir(), "director.json")
	var stdout, stderr bytes.Buffer
	if err := directorCommand([]string{score, "-o", output}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if want := output + ": 48000 Hz, states 2, macros 1, stingers 1, transitions 2\n"; stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("report %q (stderr %q), want %q", stdout.String(), stderr.String(), want)
	}
	// The file is what examples/game-director writes for the same score.
	inspection, err := inspectScore(score)
	if err != nil || inspection.semantic == nil {
		t.Fatalf("inspect: %v", err)
	}
	want, err := project.DirectorSurfaceOf(inspection.semantic, 48000)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(output); err != nil || !bytes.Equal(data, append(expected, '\n')) {
		t.Fatalf("manifest bytes differ from DirectorSurfaceOf's: %v", err)
	}
	surface := directorSurface(t, output)
	if !reflect.DeepEqual(surface, want) {
		t.Fatalf("manifest lost data in the round trip:\n%+v\n%+v", surface, want)
	}
	if surface.SampleRate != 48000 || surface.Tracks != 3 || surface.Land != "bar" || surface.PhraseBars != 8 {
		t.Errorf("header: %+v", surface)
	}
	if len(surface.States) != 2 || surface.States[0] != (director.State{Name: "explore", ID: 0, Scene: 0}) || surface.States[1] != (director.State{Name: "combat", ID: 1, Scene: 1}) {
		t.Errorf("states: %+v", surface.States)
	}
	if len(surface.Macros) != 1 || surface.Macros[0] != (director.Macro{Name: "intensity", ID: 0, SmoothFrames: 19200}) {
		t.Errorf("macros: %+v", surface.Macros)
	}
	if len(surface.Stingers) != 1 || surface.Stingers[0] != (director.Stinger{Name: "pickup", Track: 2, Slot: 0, Quantize: "beat", CrossfadeFrames: 480}) {
		t.Errorf("stingers: %+v", surface.Stingers)
	}
	if len(surface.Transitions) != 2 || surface.Transitions[1] != (director.Transition{From: "combat", To: "explore", Quantize: "phrase", CrossfadeFrames: 19200}) || len(surface.Setup) == 0 {
		t.Errorf("transitions: %+v, %d setup commands", surface.Transitions, len(surface.Setup))
	}
}

func TestDirectorCommandRate(t *testing.T) {
	score := writeDirectorScore(t, directorScore)
	for _, tc := range []struct {
		rate                  string
		smooth, stinger, fade uint32
	}{
		{"44100", 17640, 441, 17640},
		{"48000", 19200, 480, 19200},
		{"96000", 38400, 960, 38400},
	} {
		output := filepath.Join(t.TempDir(), "director.json")
		var stdout bytes.Buffer
		if err := directorCommand([]string{score, "-o", output, "--rate", tc.rate}, &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("--rate %s: %v", tc.rate, err)
		}
		surface := directorSurface(t, output)
		if surface.Macros[0].SmoothFrames != tc.smooth || surface.Stingers[0].CrossfadeFrames != tc.stinger || surface.Transitions[1].CrossfadeFrames != tc.fade || !strings.Contains(stdout.String(), tc.rate+" Hz") {
			t.Errorf("--rate %s: %+v %q", tc.rate, surface, stdout.String())
		}
	}
	for _, rate := range []string{"0", "22050", "44101", "192000", "-48000"} {
		output := filepath.Join(t.TempDir(), "director.json")
		err := directorCommand([]string{score, "-o", output, "--rate=" + rate}, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || err.Error() != "--rate must be 44100, 48000 or 96000" {
			t.Errorf("--rate %s: %v", rate, err)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Errorf("--rate %s wrote a manifest", rate)
		}
	}
}

// portableRates are the rates of the WAM2 export's images and of the manifest.
// The command and the browser check take theirs from one list, so it must not
// drift from either.
func TestPortableRatesAreTheRatesTheExportsUse(t *testing.T) {
	score := writeDirectorScore(t, directorScore)
	kernel, sdk := wam2Inputs(t)
	plugin := filepath.Join(t.TempDir(), "plugin")
	if err := wam2Command([]string{score, "-o", plugin, "--kernel", kernel, "--sdk", sdk, "--midi-track", "bed"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(plugin, "score.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.Images) != len(portableRates) {
		t.Errorf("wam2 exports %d images, the list has %d rates", len(exported.Images), len(portableRates))
	}
	inspection, err := inspectScore(score)
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{0, 8000, 22050, 32000, 44099, 44100, 44101, 47999, 48000, 48001, 88200, 95999, 96000, 96001, 192000} {
		_, accepted := exported.Images[strconv.Itoa(rate)]
		_, surfaceErr := project.DirectorSurfaceOf(inspection.semantic, rate)
		listed := slices.Contains(portableRates, rate)
		if listed != accepted || listed != (surfaceErr == nil) {
			t.Errorf("%d Hz: listed %t, exported %t, manifest accepts %t", rate, listed, accepted, surfaceErr == nil)
		}
	}
	for _, rate := range portableRates {
		if _, ok := exported.Images[strconv.Itoa(rate)]; !ok {
			t.Errorf("wam2 does not export %d Hz", rate)
		}
	}
}

// The JavaScript SDK must accept the file and send the bytes the Go SDK sends.
func TestDirectorManifestDrivesBothSDKs(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for the Game Director SDK contract test")
	}
	score := writeDirectorScore(t, directorScore)
	manifest := filepath.Join(t.TempDir(), "director.json")
	if err := directorCommand([]string{score, "-o", manifest}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	surface := directorSurface(t, manifest)
	var sent []string
	game, err := director.New(surface, func(c cmd.Command) error {
		encoded, err := cmd.EncodeCommand(c, surface.Tracks)
		sent = append(sent, hex.EncodeToString(encoded[:]))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		game.Setup,
		func() error { return game.SetState("explore", 0) },
		func() error { return game.SetMacro("intensity", 0.9, 0) },
		func() error { return game.TriggerStinger("pickup", 0) },
		func() error { return game.SetState("combat", 0) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	const script = `import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
const { GameDirector } = await import(pathToFileURL(process.argv[2]).href);
const manifest = JSON.parse(readFileSync(process.argv[3], 'utf8'));
const sent = [];
const game = new GameDirector(manifest, bytes => sent.push(Buffer.from(bytes).toString('hex')));
game.setup();
game.setState('explore');
game.setMacro('intensity', 0.9);
game.triggerStinger('pickup');
game.setState('combat');
console.log(JSON.stringify(sent));
`
	dir := t.TempDir()
	runner := filepath.Join(dir, "director.mjs")
	if err := os.WriteFile(runner, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	sdk, err := filepath.Abs(filepath.Join("..", "..", "sdk", "js", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, runner, sdk, manifest).CombinedOutput()
	if err != nil {
		t.Fatalf("JavaScript SDK rejected the manifest: %v\n%s", err, output)
	}
	var got []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &got); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if len(sent) < 5 || !slices.Equal(got, sent) {
		t.Fatalf("JavaScript sent\n%v\nGo sent\n%v", got, sent)
	}
}

func TestDirectorCommandUsage(t *testing.T) {
	score := writeDirectorScore(t, directorScore)
	output := filepath.Join(t.TempDir(), "director.json")
	const usage = "usage: cicada director <score.cicada> -o <director.json> [--rate <Hz>]"
	for _, args := range [][]string{
		nil,
		{score},
		{"-o", output},
		{score, "-o"},
		{score, "-o", filepath.Join(filepath.Dir(output), "director.txt")},
		{score, "-o", score},
		{filepath.Join(filepath.Dir(score), "game.json"), "-o", output},
		{score, score, "-o", output},
		{score, "-o", output, "extra"},
		{score, "-o", output, "--bogus"},
		{score, "-o", output, "--rate", "fast"},
	} {
		if err := directorCommand(args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || err.Error() != usage {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Error("a refused command wrote a manifest")
	}
	// The flags may come first.
	if err := directorCommand([]string{"-o", output, "--rate", "44100", score}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if surface := directorSurface(t, output); surface.SampleRate != 44100 {
		t.Errorf("rate %d", surface.SampleRate)
	}
}

func TestDirectorCommandRefusesBadScores(t *testing.T) {
	output := filepath.Join(t.TempDir(), "director.json")
	var stdout, stderr bytes.Buffer
	noLive := writeBrowserScore(t, browserBandSource)
	if err := directorCommand([]string{noLive, "-o", output}, &stdout, &stderr); err == nil || err.Error() != noLive+": score has no live block" {
		t.Fatalf("score without a live block: %v", err)
	}
	broken := writeDirectorScore(t, strings.Replace(directorScore, "state combat = fight", "state combat = missing", 1))
	err := directorCommand([]string{broken, "-o", output}, &stdout, &stderr)
	if err == nil || err.Error() != broken+" has errors" || !strings.Contains(stderr.String(), broken+":") || !strings.Contains(stderr.String(), "error CICADA-DIRECTOR: unknown state scene missing") {
		t.Fatalf("score with an error: %v\n%s", err, stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Error("a refused score wrote a manifest")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout %q", stdout.String())
	}
}

// The browser kernel runs 16 tracks. A manifest for more is refused with that
// limit named, because the surface check reports only an invalid surface.
func TestDirectorCommandNamesTheTrackLimit(t *testing.T) {
	withTracks := func(n int) string {
		var extra strings.Builder
		for i := 3; i < n; i++ { // directorScore declares three
			extra.WriteString("track extra" + strconv.Itoa(i) + " acid {}\n")
		}
		return strings.Replace(directorScore, "live {", extra.String()+"live {", 1)
	}
	output := filepath.Join(t.TempDir(), "director.json")
	var stdout, stderr bytes.Buffer
	if err := directorCommand([]string{writeDirectorScore(t, withTracks(16)), "-o", output}, &stdout, &stderr); err != nil {
		t.Fatalf("16 tracks: %v\n%s", err, stderr.String())
	}
	if surface := directorSurface(t, output); surface.Tracks != 16 {
		t.Errorf("manifest names %d tracks, want 16", surface.Tracks)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	score := writeDirectorScore(t, withTracks(17))
	err := directorCommand([]string{score, "-o", output}, &stdout, &stderr)
	want := score + ": a Game Director manifest supports at most 16 tracks, and this score has 17; remove or merge tracks (cicada check --profile browser lists them)"
	if err == nil || err.Error() != want {
		t.Fatalf("17 tracks: %v\nwant %s", err, want)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Error("a refused score wrote a manifest")
	}
}

func TestDirectorCommandReplacesAnExistingManifestAtomically(t *testing.T) {
	score := writeDirectorScore(t, directorScore)
	dir := t.TempDir()
	output := filepath.Join(dir, "director.json")
	if err := os.WriteFile(output, []byte("stale"), 0o640); err != nil {
		t.Fatal(err)
	}
	var first []byte
	for run := 0; run < 2; run++ {
		if err := directorCommand([]string{score, "-o", output}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(output)
		if err != nil || bytes.Contains(data, []byte("stale")) {
			t.Fatalf("run %d: %v", run, err)
		}
		if run == 0 {
			first = data
		} else if !bytes.Equal(first, data) {
			t.Fatal("the manifest differs between runs")
		}
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v %v", info, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Errorf("temporary files left behind: %v %v", entries, err)
	}
}

func TestDirectorCommandReadsAProjectFromAnyListedSource(t *testing.T) {
	root := t.TempDir()
	// The live block lives in the second file; the scenes and tracks in the first.
	live := directorScore[strings.Index(directorScore, "live {"):strings.Index(directorScore, "pattern bed-loop")]
	entry := strings.Replace(directorScore, live, "", 1)
	for name, source := range map[string]string{
		"cicada.mod":   "project game\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"music.cicada\"\n",
		"main.cicada":  entry,
		"music.cicada": live,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var manifests [2][]byte
	for i, name := range []string{"main.cicada", "music.cicada"} {
		output := filepath.Join(t.TempDir(), "director.json")
		var stderr bytes.Buffer
		if err := directorCommand([]string{filepath.Join(root, name), "-o", output}, &bytes.Buffer{}, &stderr); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, stderr.String())
		}
		manifests[i], _ = os.ReadFile(output)
	}
	if len(manifests[0]) == 0 || !bytes.Equal(manifests[0], manifests[1]) {
		t.Fatal("a listed source and the entry give different manifests")
	}
}
