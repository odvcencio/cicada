package project

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

func TestStdKeysCatalogUsesEveryNativePatch(t *testing.T) {
	data, err := fs.ReadFile(standardLibraries, "std/keys/cicada.mod")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := edition.ParseProjectManifest(data)
	if err != nil || manifest.Library != "std/keys" || manifest.Edition != 2 || manifest.EngineEdition != 2 || manifest.Capabilities != 512 || manifest.License != "MIT" || manifest.Author != "Cicada project" {
		t.Fatalf("keys library metadata: %+v %v", manifest, err)
	}
	data, err = fs.ReadFile(standardLibraries, "std/keys/keys.cicada")
	if err != nil {
		t.Fatal(err)
	}
	declaration := regexp.MustCompile(`preset\s+([a-z_]+)\s*\{\s*instrument\s*=\s*builtin\.([a-z_]+)\s*\}`)
	patches := declaration.FindAllStringSubmatch(string(data), -1)
	if len(patches) != len(keyboard.Names) || strings.TrimSpace(declaration.ReplaceAllString(string(data), "")) != "" {
		t.Fatal("keys library must contain exactly one native preset per original patch")
	}
	seen := map[string]bool{}
	for _, patch := range patches {
		if patch[1] != patch[2] || keyboard.ID(patch[2]) == 0 || seen[patch[1]] {
			t.Fatalf("unexpected native preset mapping: %v", patch)
		}
		seen[patch[1]] = true
	}
	for _, name := range keyboard.Names {
		if !seen[name] {
			t.Errorf("native patch %s is not exported", name)
		}
	}
}

func TestStdKeysExamplesCompileAsNativeVoices(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			score, ds, err := LoadScore(stdKeysExamplePath(name), nil)
			if err != nil || score == nil || hasErrors(ds) {
				t.Fatalf("imported example: %v %+v", err, ds)
			}
			p, ds := FromScore(score)
			if p == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Tracks != 1 || cfg.Track[0].Kind != engine.VoiceKeys || cfg.Track[0].Keys.Patch != keyboard.ID(name) || cfg.Track[0].Keys.Controls[127] != 8 {
				t.Fatal("imported example did not compile to its native keyboard voice")
			}
		})
	}
}

func stdKeysExamplePath(name string) string {
	return filepath.Join("..", "examples", "keys", "std", name+".cicada")
}
