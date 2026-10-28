package main

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// subfolderProject writes the shape of the Bon Voyage audio project: the
// manifest names main.cicada as the entry and lists a score in a subfolder, and
// the pack sits under the project folder, not under the subfolder. It returns
// the project folder.
func subfolderProject(t *testing.T) (root string, pack testpack.Pack) {
	t.Helper()
	root = t.TempDir()
	pack = testpack.Write(t, root, testpack.Options{})
	files := map[string]string{
		"cicada.mod":         "project subfolder\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"songs/theme.cicada\"\n",
		"main.cicada":        "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\nscene main { lead = melody }\nsong { main }\n",
		"songs/theme.cicada": "pattern melody notes { c4 . c5 . }\n",
	}
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, pack
}

const mainChildArgs = "CICADA_MAIN_CHILD_ARGS"

// TestMainChild is the child of runMain: it runs the real main with the
// arguments in the environment.
func TestMainChild(t *testing.T) {
	args := os.Getenv(mainChildArgs)
	if args == "" {
		t.Skip("runs only as the child of runMain")
	}
	os.Args = append([]string{"cicada"}, strings.Split(args, "\x1f")...)
	main()
}

// runMain runs the real main in dir and returns its combined output and exit
// status, so that a command's own os.Exit is part of the test.
func runMain(t *testing.T, dir string, args ...string) (output string, status int) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestMainChild$")
	child.Dir = dir
	child.Env = append(os.Environ(), mainChildArgs+"="+strings.Join(args, "\x1f"))
	combined, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(combined), exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(combined), 0
}

// A sampler pack path is project-relative, as the field's description says, and
// every command that opens a pack resolves it the way it resolves audio assets:
// from the folder of cicada.mod, not from the folder of the score it was given.
func TestCheckResolvesPackPathsFromTheProjectFolder(t *testing.T) {
	root, _ := subfolderProject(t)
	score := filepath.Join(root, "songs", "theme.cicada")
	for _, profile := range [][]string{nil, {"--profile", "browser"}} {
		var stdout, stderr bytes.Buffer
		if err := checkCommand(append(append([]string{}, profile...), score), &stdout, &stderr); err != nil {
			t.Errorf("check %v of a score listed in cicada.mod: %v\n%s", profile, err, stderr.String())
		}
	}
	// The entry at the project folder agrees, so the project is not the problem.
	if err := checkPaths([]string{filepath.Join(root, "main.cicada")}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// A pack that is missing from the project folder is still reported.
	if err := os.RemoveAll(filepath.Join(root, "packs")); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if err := checkPaths([]string{score}, &bytes.Buffer{}, &stderr); err == nil || !strings.Contains(stderr.String(), "CICADA-SAMPLER-PACK") {
		t.Fatalf("a missing pack passed: %v\n%s", err, stderr.String())
	}
}

func TestRenderAndStemsResolvePackPathsFromTheProjectFolder(t *testing.T) {
	root, _ := subfolderProject(t)
	out := t.TempDir()
	wav := filepath.Join(out, "theme.wav")
	if output, status := runMain(t, root, "render", "songs/theme.cicada", "-o", wav, "--tail", "0s"); status != 0 {
		t.Fatalf("render of a score listed in cicada.mod exited %d:\n%s", status, output)
	}
	if info, err := os.Stat(wav); err != nil || info.Size() < 1000 {
		t.Fatalf("render wrote no audio: %v", err)
	}
	stems := filepath.Join(out, "stems")
	if output, status := runMain(t, root, "stems", "songs/theme.cicada", "-o", stems, "--tail", "0s"); status != 0 {
		t.Fatalf("stems of a score listed in cicada.mod exited %d:\n%s", status, output)
	}
	if entries, err := os.ReadDir(stems); err != nil || len(entries) == 0 {
		t.Fatalf("stems wrote no files: %v", err)
	}
}

func TestAuditionResolvesPackPathsFromTheProjectFolder(t *testing.T) {
	root, _ := subfolderProject(t)
	t.Chdir(root)
	var stdout, stderr bytes.Buffer
	if err := auditionCommand([]string{"songs/theme.cicada", "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("audition of a score listed in cicada.mod: %v\n%s", err, stderr.String())
	}
}

func TestWAM2ExportResolvesPackPathsFromTheProjectFolder(t *testing.T) {
	root, _ := subfolderProject(t)
	if err := exportWAM2(t, filepath.Join(root, "songs", "theme.cicada")); err != nil {
		t.Fatalf("WAM2 export of a score listed in cicada.mod: %v", err)
	}
}

// receiveOptions waits for the render options that an export handed its runner.
func receiveOptions(t *testing.T, from <-chan render.Options) render.Options {
	t.Helper()
	select {
	case options := <-from:
		return options
	case <-time.After(30 * time.Second):
		t.Fatal("the export never started its render")
		return render.Options{}
	}
}

// Studio opened on a score in a subfolder exports with the same packs as the
// entry score: both exports hand the renderer the project folder.
func TestStudioExportsResolvePackPathsFromTheProjectFolder(t *testing.T) {
	root, _ := subfolderProject(t)
	s, err := newStudio(filepath.Join(root, "songs", "theme.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	handler := s.routes()

	audio := make(chan render.Options, 1)
	s.exports.render = func(_ *notation.Score, _ string, options render.Options, _ renderTargetOptions, _ func(int)) (loudnessFileReport, error) {
		audio <- options
		return loudnessFileReport{}, nil
	}
	reply := studioCall(t, handler, "/api/export", studioExportRequest{TargetLUFS: -14, TruePeakMax: -1, Tolerance: .5, Rate: 48000, Bits: 24})
	if reply.Code != http.StatusAccepted {
		t.Fatalf("export: %d %s", reply.Code, reply.Body.String())
	}
	if options := receiveOptions(t, audio); options.AssetRoot != root || options.AssetDir != root {
		t.Errorf("the audio export resolves assets and packs from %q and %q, want the project folder %q", options.AssetRoot, options.AssetDir, root)
	}

	stems := make(chan render.Options, 1)
	s.stemsExport.render = func(_ *notation.Score, options render.Options, dir string) (render.Report, error) {
		stems <- options
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return render.Report{}, err
		}
		return render.Report{}, os.WriteFile(filepath.Join(dir, "master.wav"), []byte("x"), 0o600)
	}
	reply = studioCall(t, handler, "/api/export/stems", studioStemsExportRequest{SourceRevision: studioProjectReceipt(t, filepath.Join(root, "songs", "theme.cicada")), Rate: 48000})
	if reply.Code != http.StatusAccepted {
		t.Fatalf("stems export: %d %s", reply.Code, reply.Body.String())
	}
	if options := receiveOptions(t, stems); options.AssetRoot != root || options.AssetDir != root {
		t.Errorf("the stems export resolves assets and packs from %q and %q, want the project folder %q", options.AssetRoot, options.AssetDir, root)
	}
}
