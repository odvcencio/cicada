package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/internal/testwav"
)

// writeKitSession writes a small recording session: takes made in the test
// and a recipe that names them. It returns the recipe's path.
func writeKitSession(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "session")
	if err := os.MkdirAll(filepath.Join(dir, "takes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"takes/kick-soft.wav": testwav.Take(48000, testwav.Kick, .08, .09, .10),
		"kick-hard.wav":       testwav.Take(48000, testwav.Kick, .78, .80, .82),
		"closed-hat.wav":      testwav.Take(48000, testwav.ClosedHat, .35, .38, .36),
		"open-hat.wav":        testwav.Take(48000, testwav.OpenHat, .40, .42, .41),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recipe := `{
  "format": "cicada.kit-recipe/1",
  "pieces": [
    {"name": "kick", "note": 36, "layers": 2, "takes": ["takes/kick-soft.wav", "kick-hard.wav"]},
    {"name": "closed_hat", "note": 42, "layers": 1, "choke": "hat", "takes": ["closed-hat.wav"]},
    {"name": "open_hat", "note": 46, "layers": 1, "choke": "hat", "takes": ["open-hat.wav"]}
  ]
}
`
	path := filepath.Join(dir, "kit.json")
	if err := os.WriteFile(path, []byte(recipe), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecordKitBuildsOnePackFromARecipe(t *testing.T) {
	recipe := writeKitSession(t)
	dir := filepath.Join(t.TempDir(), "packs", "studio")
	var output bytes.Buffer
	if err := recordKitCommand([]string{"-o", dir, "--name", "studio", recipe}, &output); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	pin := recording.Digest(manifest)
	for _, want := range []string{
		"3 pieces · 12 hits · 12 zones · owner recording",
		"kick", "c2 36", "closed_hat", "gb2 42", "open_hat", "bb2 46", "3 at ", "hat",
		"sampler studio {", `sha256 = "` + pin + `"`, "root = c2", "voices = 16",
		"manifest.json", "starter score: " + filepath.Join(dir, "instrument.cicada"),
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("receipt lacks %q:\n%s", want, output.String())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".wav.gz") {
			hits++
		}
	}
	if hits != 12 || len(entries) != 16 {
		t.Fatalf("%d hit files among %d files, want 12 among 16", hits, len(entries))
	}
	for _, name := range []string{"manifest.json", "analysis.json", "instrument.cicada", "LICENSE.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := instrumentpack.Load(dir, "manifest.json", pin); err != nil {
		t.Fatal("the written pack does not load against the printed pin:", err)
	}
	// The same recipe and takes rebuild into the same directory, byte for
	// byte; a re-recorded take needs a new directory.
	var again bytes.Buffer
	if err := recordKitCommand([]string{"-o", dir, "--name", "studio", recipe}, &again); err != nil {
		t.Fatal("identical rebuild refused:", err)
	}
	if again.String() != output.String() {
		t.Fatalf("second receipt differs:\n%s\n%s", output.String(), again.String())
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(recipe), "kick-hard.wav"), testwav.Take(48000, testwav.Kick, .70, .72, .74), 0o600); err != nil {
		t.Fatal(err)
	}
	err = recordKitCommand([]string{"-o", dir, "--name", "studio", recipe}, &again)
	if err == nil || !strings.Contains(err.Error(), "already exists with different contents") {
		t.Fatalf("changed takes were written over an existing pack: %v", err)
	}
}

func TestRecordKitRefusesWithClearErrors(t *testing.T) {
	recipe := writeKitSession(t)
	folder := filepath.Dir(recipe)
	badJSON := filepath.Join(folder, "bad.json")
	if err := os.WriteFile(badJSON, []byte(`{"format": "cicada.kit-recipe/1", "pieces": [{"name": "kick", "note": 36, "takes": ["a.wav"], "layerz": 2}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(folder, "missing.json")
	if err := os.WriteFile(missing, []byte(`{"format": "cicada.kit-recipe/1", "pieces": [{"name": "kick", "note": 36, "takes": ["kick-medium.wav"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tooFew := filepath.Join(folder, "few.json")
	if err := os.WriteFile(tooFew, []byte(`{"format": "cicada.kit-recipe/1", "pieces": [{"name": "kick", "note": 36, "layers": 4, "takes": ["kick-hard.wav"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := func() string { return filepath.Join(t.TempDir(), "pack") }
	cases := []struct {
		name string
		args func(out string) []string
		want string
	}{
		{"no output directory", func(string) []string { return []string{recipe} }, "usage: cicada record-kit -o <pack-directory>"},
		{"no recipe", func(o string) []string { return []string{"-o", o} }, "usage: cicada record-kit -o <pack-directory>"},
		{"flags after the recipe", func(o string) []string { return []string{"-o", o, recipe, "--name", "x"} }, "put flags before the recipe"},
		{"two recipes", func(o string) []string { return []string{"-o", o, recipe, recipe} }, "give one recipe file"},
		{"unknown flag", func(o string) []string { return []string{"-o", o, "--layers", "3", recipe} }, "flag provided but not defined: -layers"},
		{"recipe not found", func(o string) []string { return []string{"-o", o, filepath.Join(folder, "nope.json")} }, "no such file"},
		{"misspelled option", func(o string) []string { return []string{"-o", o, badJSON} }, `bad.json: unknown field "layerz"`},
		{"missing take", func(o string) []string { return []string{"-o", o, missing} }, `piece "kick": take "kick-medium.wav": not found`},
		{"too few hits for the layers", func(o string) []string { return []string{"-o", o, tooFew} }, `piece "kick": found 3 hits, but 4 velocity layers need at least 4`},
		{"reserved name", func(o string) []string { return []string{"-o", o, "--name", "drums", recipe} }, "reserved by scores"},
		{"invalid name", func(o string) []string { return []string{"-o", o, "--name", "My Kit", recipe} }, "use a lowercase kit name"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := out()
			err := recordKitCommand(test.args(dir), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v; want it to mention %q", err, test.want)
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Fatal("a refused build left a pack directory behind")
			}
		})
	}
}

func TestRecordKitHelp(t *testing.T) {
	var byHelp, byFlag, stderr bytes.Buffer
	if handled, code := handleCLIHelp([]string{"help", "record-kit"}, &byHelp, &stderr); !handled || code != 0 {
		t.Fatalf("help record-kit: handled=%t code=%d stderr=%q", handled, code, stderr.String())
	}
	if handled, code := handleCLIHelp([]string{"record-kit", "--help"}, &byFlag, &stderr); !handled || code != 0 {
		t.Fatalf("record-kit --help: handled=%t code=%d stderr=%q", handled, code, stderr.String())
	}
	if byHelp.String() != byFlag.String() {
		t.Fatalf("the two spellings differ:\n%s\n%s", byHelp.String(), byFlag.String())
	}
	for _, want := range []string{
		"cicada record-kit -o <pack-directory> [flags] <recipe.json>",
		"build one drum-kit sampler pack from your own recorded takes",
		"-o <directory>", "--name <id>", "--help", "Put flags before the recipe", "owner recording",
	} {
		if !strings.Contains(byHelp.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, byHelp.String())
		}
	}
	var global bytes.Buffer
	handleCLIHelp([]string{"--help"}, &global, &stderr)
	if !strings.Contains(global.String(), "record-pack, record-kit, fit-model") {
		t.Errorf("the command list lacks record-kit:\n%s", global.String())
	}
}
