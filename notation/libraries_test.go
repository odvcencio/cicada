package notation

import (
	"fmt"
	"strings"
	"testing"
)

const modeledKitBody = "kit band_kit { bd = model.kick ch = model.hat_closed }\npattern beat drums steps=4 { bd: x... ch: x.x. }\n"
const modeledKitUse = "track drums %s {}\nscene main { drums = %s }\nsong { main }\n"

func TestModeledKitTargetsResolveInEveryScope(t *testing.T) {
	use := func(kit, pattern string) string { return fmt.Sprintf(modeledKitUse, kit, pattern) }
	library := func(bindings map[string]string) SourceFile {
		return SourceFile{Path: "band.cicada", Library: "demo/band", Edition: 2, Bindings: bindings, Source: []byte(modeledKitBody)}
	}
	for _, test := range []struct {
		name  string
		files []SourceFile
		kit   string
	}{
		{"loose score", []SourceFile{{Path: "main.cicada", Source: []byte(modeledKitBody + use("band_kit", "beat"))}}, "band_kit"},
		{"score with an import", []SourceFile{{Path: "main.cicada", Bindings: map[string]string{"fx": "std.fx"}, Source: []byte(modeledKitBody + use("band_kit", "beat"))}}, "band_kit"},
		{"library without imports", []SourceFile{{Path: "main.cicada", Bindings: map[string]string{"band": "demo.band"}, Source: []byte(use("band.band_kit", "band.beat"))}, library(map[string]string{})}, "demo.band.band_kit"},
		{"library with an import", []SourceFile{{Path: "main.cicada", Bindings: map[string]string{"band": "demo.band"}, Source: []byte(use("band.band_kit", "band.beat"))}, library(map[string]string{"fx": "std.fx"})}, "demo.band.band_kit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			score, diagnostics := ParseFiles(test.files, 2)
			if score == nil || len(diagnostics) != 0 {
				t.Fatalf("diagnostics: %+v", diagnostics)
			}
			if len(score.Kits) != 1 || score.Kits[0].Name != test.kit || len(score.Kits[0].Bindings) != 2 {
				t.Fatalf("kit: %+v", score.Kits)
			}
			if targets := score.Kits[0].Bindings; targets[0].Target != "model.kick" || targets[1].Target != "model.hat_closed" {
				t.Fatalf("modeled targets were rewritten: %+v", targets)
			}
		})
	}
}

func TestImportBoundToModelOwnsTheNamespace(t *testing.T) {
	files := []SourceFile{
		{Path: "main.cicada", Bindings: map[string]string{"model": "acme.model"}, Source: []byte("kit band_kit { bd = model.kick }\npattern beat drums steps=4 { bd: x... }\n" + fmt.Sprintf(modeledKitUse, "band_kit", "beat"))},
		{Path: "model.cicada", Library: "acme/model", Edition: 2, Bindings: map[string]string{}, Source: []byte("instrument kick { voice mono { out = sine(pitch) * env(gate, 90ms) } }\n")},
	}
	score, diagnostics := ParseFiles(files, 2)
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
	if target := score.Kits[0].Bindings[0].Target; target != "acme.model.kick" {
		t.Fatalf("bound import lost the namespace: %s", target)
	}
}

// A kit lane reads every target that starts with model. as an engine piece, so a
// library whose path is model, or starts with model/, cannot supply kit
// instruments. The manual says so; reserving the name would change this test.
func TestLibraryPathsNamedModelDoNotReachLibraryInstruments(t *testing.T) {
	for _, test := range []struct {
		name, path, namespace, target, diagnostic string
	}{
		{"import model", "model", "model", "model.kick", ""},
		{"library under model", "model/perc", "perc", "model.perc.kick", "CICADA-REFERENCE: unknown modeled drum model.perc.kick"},
		{"library under another path", "acme/perc", "perc", "acme.perc.kick", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := []SourceFile{
				{Path: "main.cicada", Bindings: map[string]string{test.namespace: strings.ReplaceAll(test.path, "/", ".")}, Source: []byte(
					"kit band_kit { bd = " + test.namespace + ".kick }\npattern beat drums steps=4 { bd: x... }\n" + fmt.Sprintf(modeledKitUse, "band_kit", "beat"))},
				{Path: "lib.cicada", Library: test.path, Edition: 2, Bindings: map[string]string{}, Source: []byte("instrument kick { voice mono { out = sine(pitch) * env(gate, 90ms) } }\n")},
			}
			score, diagnostics := ParseFiles(files, 2)
			if score == nil || len(score.Kits) != 1 || len(score.Kits[0].Bindings) != 1 || score.Kits[0].Bindings[0].Target != test.target {
				t.Fatalf("want target %s, got %+v %+v", test.target, score, diagnostics)
			}
			var messages []string
			for _, d := range diagnostics {
				messages = append(messages, d.Code+": "+d.Message)
			}
			if got := strings.Join(messages, "; "); got != test.diagnostic {
				t.Fatalf("diagnostics %q, want %q", got, test.diagnostic)
			}
		})
	}
}

func TestUnboundNamespacesStillFailInImportScopes(t *testing.T) {
	for _, test := range []struct{ name, target, code, message string }{
		{"misspelled namespace", "modle.kick", "CICADA-LIB-REFERENCE", "no import binds namespace modle"},
		{"unknown piece", "model.zzz", "CICADA-REFERENCE", "unknown modeled drum model.zzz"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := strings.Replace(modeledKitBody, "model.kick", test.target, 1) + fmt.Sprintf(modeledKitUse, "band_kit", "beat")
			_, diagnostics := ParseFiles([]SourceFile{{Path: "main.cicada", Bindings: map[string]string{"fx": "std.fx"}, Source: []byte(source)}}, 2)
			found := false
			for _, d := range diagnostics {
				found = found || d.Code == test.code && strings.Contains(d.Message, test.message) && d.Position.Line == 1
				if test.code != "CICADA-LIB-REFERENCE" && d.Code == "CICADA-LIB-REFERENCE" {
					t.Fatalf("an import error hides the modeled piece diagnostic: %+v", d)
				}
			}
			if !found {
				t.Fatalf("want %s %q on line 1, got %+v", test.code, test.message, diagnostics)
			}
		})
	}
}
