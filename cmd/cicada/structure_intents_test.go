package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/render"
)

// TestStructureRefusalsAreData checks that a structural refusal reaches
// apply --json as a reason, the field at fault and the uses that block it.
func TestStructureRefusalsAreData(t *testing.T) {
	for _, tc := range []struct {
		name, intent, reason, field, use string
	}{
		{"pattern in use", `{"kind":"deletepattern","pattern":"riff"}`, "pattern_in_use", "pattern", "scene main binding bass = riff (line 9)"},
		{"scene in use", `{"kind":"deletescene","scene":"main"}`, "scene_in_use", "scene", "song entry main*2 (line 10)"},
		{"name taken", `{"kind":"rename","entity":"track:bass","name":"lead"}`, "name_taken", "name", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err, score := runApplyJSON(t, tc.intent, "--write")
			failure := result.Error
			if err == nil || result.OK || failure == nil || failure.Code != "intent_refused" || failure.Reason != tc.reason || failure.Field != tc.field {
				t.Fatalf("%v %+v", err, failure)
			}
			if tc.use != "" && (len(failure.Uses) != 1 || failure.Uses[0] != tc.use) {
				t.Fatalf("uses %q, want %q", failure.Uses, tc.use)
			}
			if data, _ := os.ReadFile(score); string(data) != applyJSONScore {
				t.Fatal("a refused intent changed the score")
			}
		})
	}
}

// TestRenamesAndUnusedDeletesKeepTheRender renames a track, a pattern and a
// scene of the intent-catalog fixture, which name them in automation, a
// modulation route, scene settings, the song and a MIDI mapping, and deletes
// its unused pattern and scene, in one envelope. The render must stay
// byte-identical.
func TestRenamesAndUnusedDeletesKeepTheRender(t *testing.T) {
	score := catalogProject(t, "rename")
	dir := filepath.Dir(score)
	opts := render.Options{SampleRate: 48000, Bits: 16, Bars: 6, Block: 4096}
	first, code, stderr := runRenderJSON(t, "render", score, filepath.Join(dir, "before.wav"), opts, nil)
	if code != 0 || !first.OK {
		t.Fatalf("render before: %d %s", code, stderr)
	}
	envelope := filepath.Join(dir, "structure.json")
	intents := []string{
		`{"kind":"rename","entity":"track:bass","name":"sub"}`,
		`{"kind":"rename","entity":"pattern:riff","name":"groove"}`,
		`{"kind":"rename","entity":"scene:main","name":"verse"}`,
		`{"kind":"rename","entity":"track:lead","name":"melody"}`,
		`{"kind":"deletepattern","pattern":"fill"}`,
		`{"kind":"deletescene","scene":"outro"}`,
	}
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[`+strings.Join(intents, ",")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyCommandWithHooks([]string{score, envelope, "--write"}, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	edited, err := os.ReadFile(score)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"track bass", "riff", "scene main", "track lead", "lead.", "pattern fill", "scene outro"} {
		if bytes.Contains(edited, []byte(gone)) {
			t.Fatalf("%q is left in the edited score:\n%s", gone, edited)
		}
	}
	second, code, stderr := runRenderJSON(t, "render", score, filepath.Join(dir, "after.wav"), opts, nil)
	if code != 0 || !second.OK {
		t.Fatalf("render after: %d %s", code, stderr)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "before.wav"))
	after, _ := os.ReadFile(filepath.Join(dir, "after.wav"))
	if len(before) == 0 || !bytes.Equal(before, after) {
		t.Fatalf("the render changed: %d bytes before, %d after", len(before), len(after))
	}
}

// writeProject writes files into a new folder and returns it.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// applyIntents runs apply --write --json on score and decodes the result.
func applyIntents(t *testing.T, score, intents string) applyJSONResult {
	t.Helper()
	var result applyJSONResult
	if err := json.Unmarshal([]byte(probeApply(t, score, intents)), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestDeletingATrackThatReseedsRandomChoicesNeedsReseed: the engine seeds
// chance by a track's position, so deleting the unused track pad, which sits
// before hat, changes hat's x?50 hits. The delete is refused without reseed;
// with it, it applies and the result's warnings say what changes. Without
// chance in the pattern nothing is reseeded and no flag is needed.
func TestDeletingATrackThatReseedsRandomChoicesNeedsReseed(t *testing.T) {
	chance := "cicada 2\ntempo 120\nseed 7\ntrack pad piano {}\ntrack hat drums {}\npattern chords { [a3 c4 e4] - - . }\npattern beat drums { ch: x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 }\nscene main { hat = beat }\nsong { main*2 }\n"
	dir := writeProject(t, map[string]string{"song.cicada": chance})
	result := applyIntents(t, filepath.Join(dir, "song.cicada"), `{"kind":"deletetrack","track":"pad"}`)
	if result.Error == nil || result.Error.Reason != "reseeds_random_choices" || result.Error.Field != "reseed" || len(result.Error.Uses) != 1 || !strings.Contains(result.Error.Uses[0], "track hat, pattern beat: the track moves from position 2 to 1") {
		t.Fatalf("without reseed: %+v", result.Error)
	}
	result = applyIntents(t, filepath.Join(dir, "song.cicada"), `{"kind":"deletetrack","track":"pad","reseed":true}`)
	if !result.OK || len(result.Intents) != 1 || len(result.Intents[0].Warnings) != 1 || !strings.Contains(result.Intents[0].Warnings[0], "random choices (chance and humanize) change: track hat") {
		t.Fatalf("with reseed: %+v %+v", result.Error, result.Intents)
	}
	plain := strings.ReplaceAll(chance, "x?50", "x")
	dir = writeProject(t, map[string]string{"song.cicada": plain})
	if result := applyIntents(t, filepath.Join(dir, "song.cicada"), `{"kind":"deletetrack","track":"pad"}`); !result.OK || len(result.Intents[0].Warnings) != 0 {
		t.Fatalf("without chance: %+v %+v", result.Error, result.Intents)
	}
}

// TestStudioIntentsReplyWithReseedWarnings: through Studio's /api/intents,
// the same delete is refused with its reason and uses, and with reseed it
// applies and the reply lists the warning cicada apply --json gives.
func TestStudioIntentsReplyWithReseedWarnings(t *testing.T) {
	chance := "cicada 2\ntempo 120\nseed 7\ntrack pad piano {}\ntrack hat drums {}\npattern chords { [a3 c4 e4] - - . }\npattern beat drums { ch: x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 x?50 }\nscene main { hat = beat }\nsong { main*2 }\n"
	dir := writeProject(t, map[string]string{"song.cicada": chance})
	h, err := studioHandler(filepath.Join(dir, "song.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	envelope := func(reseed string) string {
		return `{"version":1,"revision":"` + edits.Revision([]byte(chance)) + `","intents":[{"kind":"deletetrack","track":"pad"` + reseed + `}]}`
	}
	var refused struct {
		Reason string   `json:"reason"`
		Uses   []string `json:"uses"`
	}
	recorder := studioCallRaw(t, h, "/api/intents", envelope(""))
	if err := json.Unmarshal(recorder.Body.Bytes(), &refused); err != nil || recorder.Code != http.StatusUnprocessableEntity || refused.Reason != "reseeds_random_choices" || len(refused.Uses) != 1 || !strings.Contains(refused.Uses[0], "track hat, pattern beat") {
		t.Fatalf("without reseed: %d %s", recorder.Code, recorder.Body.String())
	}
	var applied struct {
		Warnings []string `json:"warnings"`
	}
	recorder = studioCallRaw(t, h, "/api/intents", envelope(`,"reseed":true`))
	if err := json.Unmarshal(recorder.Body.Bytes(), &applied); err != nil || recorder.Code != http.StatusOK || len(applied.Warnings) != 1 || !strings.Contains(applied.Warnings[0], "random choices (chance and humanize) change: track hat") {
		t.Fatalf("with reseed: %d %s", recorder.Code, recorder.Body.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "song.cicada")); strings.Contains(string(data), "track pad") {
		t.Fatal("the delete was not written")
	}
}

// TestRenameEditsEveryFileOfTheProject renames a track that another source
// file of the project automates and binds; one apply changes both files.
func TestRenameEditsEveryFileOfTheProject(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"cicada.mod":        "project mf\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/more.cicada\"\n",
		"main.cicada":       "tempo 120\ntrack bass acid {}\ntrack keys piano {}\npattern riff { a1 . c2 . }\npattern pad { [a3 c4 e4] - - . }\nscene main { bass = riff keys = pad }\nsong { main }\n",
		"parts/more.cicada": "automate keys.level { @1.1.1 -6dB }\nscene extra { keys = pad }\n",
	})
	result := applyIntents(t, filepath.Join(dir, "main.cicada"), `{"kind":"rename","entity":"track:keys","name":"organ"}`)
	if !result.OK || !result.Written || len(result.Intents) != 1 || !strings.Contains(result.Intents[0].Label, "in 2 files") {
		t.Fatalf("rename: %+v %+v", result.Error, result.Intents)
	}
	main, _ := os.ReadFile(filepath.Join(dir, "main.cicada"))
	more, _ := os.ReadFile(filepath.Join(dir, "parts", "more.cicada"))
	if string(main) != "tempo 120\ntrack bass acid {}\ntrack organ piano {}\npattern riff { a1 . c2 . }\npattern pad { [a3 c4 e4] - - . }\nscene main { bass = riff organ = pad }\nsong { main }\n" || string(more) != "automate organ.level { @1.1.1 -6dB }\nscene extra { organ = pad }\n" {
		t.Fatalf("files:\n%s\n%s", main, more)
	}
	if checked, err := runCheckJSON(t, filepath.Join(dir, "main.cicada")); err != nil || !checked.Valid {
		t.Fatalf("check after the rename: %v", err)
	}
}

// TestNewNamesThatAnImportUsesAreRefused: in a project that imports
// demo/tone, a scene, a track or a renamed entity called tone is refused with
// name_taken, not with an invalid result.
func TestNewNamesThatAnImportUsesAreRefused(t *testing.T) {
	files := map[string]string{}
	err := filepath.WalkDir(filepath.Join("..", "..", "examples", "libraries"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(filepath.Join("..", "..", "examples", "libraries"), path)
		files[filepath.ToSlash(relative)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, files)
	for _, intent := range []string{
		`{"kind":"addscene","name":"tone"}`,
		`{"kind":"duplicatescene","scene":"verse","name":"tone"}`,
		`{"kind":"addtrack","name":"tone","voice":"piano"}`,
		`{"kind":"rename","entity":"track:lead","name":"tone"}`,
	} {
		result := applyIntents(t, filepath.Join(dir, "main.cicada"), intent)
		if result.Error == nil || result.Error.Code != "intent_refused" || result.Error.Reason != "name_taken" || result.Error.Field != "name" {
			t.Fatalf("%s: %+v", intent, result.Error)
		}
	}
}

// TestRefusalsNameOtherFilesAsJSONPaths: run from the project's folder, a
// refusal's message, uses and file name another source file by its path
// relative to that folder, and the diff names it the same way.
func TestRefusalsNameOtherFilesAsJSONPaths(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"cicada.mod":        "project mf\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/more.cicada\"\n",
		"main.cicada":       "tempo 120\ntrack bass acid {}\ntrack keys piano {}\npattern riff { a1 . c2 . }\npattern pad { [a3 c4 e4] - - . }\nscene main { bass = riff keys = pad }\nsong { main }\n",
		"parts/more.cicada": "automate keys.level { @1.1.1 -6dB }\nscene extra { keys = pad }\n",
	})
	t.Chdir(dir)
	failure := applyIntents(t, "main.cicada", `{"kind":"deletetrack","track":"keys"}`).Error
	if failure == nil || failure.Reason != "track_in_use" || len(failure.Uses) != 1 || failure.Uses[0] != "automation keys.level (parts/more.cicada line 1)" || strings.Contains(failure.Message, dir) {
		t.Fatalf("%+v", failure)
	}
	var stdout bytes.Buffer
	envelope := filepath.Join(dir, "rename.json")
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[{"kind":"rename","entity":"track:keys","name":"organ"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyCommandWithHooks([]string{"main.cicada", envelope}, &stdout, nil); err != nil || !strings.Contains(stdout.String(), "--- a/parts/more.cicada\n+++ b/parts/more.cicada\n") {
		t.Fatalf("diff: %v\n%s", err, stdout.String())
	}
}

// TestEnvelopeRevisionsCoverOtherFiles: a dry run reports the revision of
// every source file it read; an envelope that carries them is refused when
// one of those files changed in between, and applies when none did.
func TestEnvelopeRevisionsCoverOtherFiles(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"cicada.mod":        "project mf\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/more.cicada\"\n",
		"main.cicada":       "tempo 120\ntrack bass acid {}\ntrack keys piano {}\npattern riff { a1 . c2 . }\npattern pad { [a3 c4 e4] - - . }\nscene main { bass = riff keys = pad }\nsong { main }\n",
		"parts/more.cicada": "automate keys.level { @1.1.1 -6dB }\nscene extra { keys = pad }\n",
	})
	t.Chdir(dir)
	rename := `{"kind":"rename","entity":"track:keys","name":"organ"}`
	var stdout bytes.Buffer
	if err := os.WriteFile("dry.json", []byte(`{"version":1,"intents":[`+rename+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyCommandWithHooks([]string{"main.cicada", "dry.json", "--json"}, &stdout, nil); err != nil {
		t.Fatal(err)
	}
	var dry struct {
		FileRevisions []edits.FileRevision `json:"file_revisions"`
	}
	paths := map[string]bool{}
	if err := json.Unmarshal(stdout.Bytes(), &dry); err == nil {
		for _, file := range dry.FileRevisions {
			paths[file.Path] = file.Revision != ""
		}
	}
	if !paths["parts/more.cicada"] || !paths["main.cicada"] {
		t.Fatalf("file_revisions: %v", dry.FileRevisions)
	}
	revisions, _ := json.Marshal(dry.FileRevisions)
	if err := os.WriteFile("parts/more.cicada", []byte("automate keys.level { @1.1.1 -9dB }\nscene extra { keys = pad }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("go.json", []byte(`{"version":1,"revisions":`+string(revisions)+`,"intents":[`+rename+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := applyCommandWithHooks([]string{"main.cicada", "go.json", "--write", "--json"}, &out, nil)
	var result applyJSONResult
	if json.Unmarshal(out.Bytes(), &result); err == nil || result.Error == nil || result.Error.Code != "revision_mismatch" || !strings.Contains(result.Error.Message, "parts/more.cicada changed") {
		t.Fatalf("a changed file was not noticed: %v %+v", err, result.Error)
	}
	if data, _ := os.ReadFile("main.cicada"); strings.Contains(string(data), "organ") {
		t.Fatal("a refused batch wrote the entry")
	}
}

// TestRevisionPathsAreProjectRelative: an envelope's revisions name files
// relative to the project folder wherever apply runs from, and a path that
// is not one of the project's sources is refused without being read. Studio
// checks the same field through the same code.
func TestRevisionPathsAreProjectRelative(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"cicada.mod":        "project mf\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/more.cicada\"\n",
		"main.cicada":       "tempo 120\ntrack bass acid {}\ntrack keys piano {}\npattern riff { a1 . c2 . }\nscene main { bass = riff keys = riff }\nsong { main }\n",
		"parts/more.cicada": "automate keys.level { @1.1.1 -6dB }\n",
	})
	more, _ := os.ReadFile(filepath.Join(dir, "parts", "more.cicada"))
	t.Chdir(filepath.Dir(dir))
	main := filepath.Join(filepath.Base(dir), "main.cicada")
	rename := `{"kind":"rename","entity":"track:keys","name":"organ"}`
	for _, tc := range []struct {
		name, revisions, code string
	}{
		{"relative to the project", `[{"path":"parts/more.cicada","revision":"` + edits.Revision(more) + `"}]`, ""},
		{"not a source", `[{"path":"/etc/hostname","revision":"x"}]`, "revision_mismatch"},
		{"changed", `[{"path":"parts/more.cicada","revision":"` + strings.Repeat("0", 64) + `"}]`, "revision_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := filepath.Join(dir, "env.json")
			if err := os.WriteFile(envelope, []byte(`{"version":1,"revisions":`+tc.revisions+`,"intents":[`+rename+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			_ = applyCommandWithHooks([]string{main, envelope, "--json"}, &out, nil)
			var result applyJSONResult
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			code := ""
			if result.Error != nil {
				code = result.Error.Code
			}
			if code != tc.code || tc.name == "not a source" && !strings.Contains(result.Error.Message, "/etc/hostname is not a source file of this project") {
				t.Fatalf("%+v", result.Error)
			}
		})
	}
	h, err := studioHandler(filepath.Join(dir, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	source, _ := os.ReadFile(filepath.Join(dir, "main.cicada"))
	body := `{"version":1,"revision":"` + edits.Revision(source) + `","revisions":[{"path":"parts/more.cicada","revision":"` + strings.Repeat("0", 64) + `"}],"intents":[` + rename + `]}`
	recorder := studioCallRaw(t, h, "/api/intents", body)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "revision_mismatch") {
		t.Fatalf("Studio ignored revisions: %d %s", recorder.Code, recorder.Body.String())
	}
}

// TestStudioDoesNotHalfUndoAnOutsideMultiFileChange: a rename made with
// cicada apply changes the score and another file. Studio sees only the
// score change, so it records it without offering Undo, and refuses an Undo
// request rather than restore the score alone.
func TestStudioDoesNotHalfUndoAnOutsideMultiFileChange(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"cicada.mod":        "project mf\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/more.cicada\"\n",
		"main.cicada":       "tempo 120\ntrack bass acid {}\ntrack keys piano {}\npattern riff { a1 . c2 . }\nscene main { bass = riff keys = riff }\nsong { main }\n",
		"parts/more.cicada": "automate keys.level { @1.1.1 -6dB }\n",
	})
	main := filepath.Join(dir, "main.cicada")
	h, err := studioHandler(main)
	if err != nil {
		t.Fatal(err)
	}
	if result := applyIntents(t, main, `{"kind":"rename","entity":"track:keys","name":"organ"}`); !result.OK || !result.Written {
		t.Fatalf("rename: %+v", result.Error)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	request.Host = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	var history struct {
		CanUndo bool `json:"canUndo"`
		Edits   []struct {
			Outside bool `json:"outside"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &history); err != nil || history.CanUndo || len(history.Edits) == 0 || !history.Edits[0].Outside {
		t.Fatalf("history offers an Undo of an outside multi-file change: %v %s", err, recorder.Body.String())
	}
	current, _ := os.ReadFile(main)
	undo := studioCall(t, h, "/api/undo", studioEdit{Revision: studioRevision(current)})
	more, _ := os.ReadFile(filepath.Join(dir, "parts", "more.cicada"))
	if undo.Code != http.StatusConflict || !strings.Contains(string(more), "organ.level") {
		t.Fatalf("undo: %d %s\n%s", undo.Code, undo.Body.String(), more)
	}
	if after, _ := os.ReadFile(main); string(after) != string(current) {
		t.Fatal("undo restored the score alone")
	}
}
