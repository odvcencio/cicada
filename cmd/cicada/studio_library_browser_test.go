//go:build browser

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/project"
)

func TestBrowserStudioLibrary(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	server := startBrowserStudio(t, []byte(studioLibraryScore), nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.click("[data-panel-tab=library]")
	chrome.waitFor("document.querySelectorAll('.library-row').length>0", 5*time.Second)
	chrome.eval(`(()=>{document.querySelector('#library-kind').value='instrument';document.querySelector('#library-kind').dispatchEvent(new Event('change'));document.querySelector('#library-query').value='glassbass';document.querySelector('#library-query').dispatchEvent(new Event('input'));document.querySelector('#library-track').value='lead';return true})()`)
	chrome.waitFor("document.querySelectorAll('.library-row').length===1", 5*time.Second)
	chrome.eval(`(()=>{const mode=document.querySelector('#audio-mode');mode.value='browser';mode.dispatchEvent(new Event('change'));return true})()`)
	chrome.click(".library-row button")
	chrome.waitFor("document.querySelector('#library-status').textContent.includes('Previewing glassbass')", 20*time.Second)
	chrome.eval(`window.__libraryRevision=document.body.dataset.revision;true`)
	chrome.click("#transport-button")
	chrome.waitFor("window.cicadaBrowserAudio.playing", 5*time.Second)
	chrome.eval(`(()=>{if(document.body.dataset.revision!==window.__libraryRevision)throw new Error('Preview wrote source');return true})()`)
	chrome.click("#transport-button")
	chrome.waitFor("!window.cicadaBrowserAudio.playing", 5*time.Second)
	chrome.click(".library-row button:last-child")
	chrome.waitFor("document.querySelector('#source-editor').value.includes('track lead synth.glassbass')", 10*time.Second)
	chrome.eval(`(()=>{document.querySelector('#library-preset-name').value='bright';document.querySelector('#library-track').value='lead';return true})()`)
	chrome.click("#library-save")
	chrome.waitFor("document.querySelector('#source-editor').value.includes('preset bright')", 10*time.Second)
	chrome.click("[data-panel-tab=history]")
	chrome.waitFor("!document.querySelector('#history-undo').disabled", 5*time.Second)
	chrome.click("#history-undo")
	chrome.waitFor("!document.querySelector('#source-editor').value.includes('preset bright')", 10*time.Second)
	chrome.waitFor("!document.querySelector('#history-redo').disabled", 5*time.Second)
	chrome.click("#history-redo")
	chrome.waitFor("document.querySelector('#source-editor').value.includes('preset bright')", 10*time.Second)
	chrome.setViewport(390, 900)
	chrome.eval(`document.querySelector("[data-panel-tab=library]").scrollIntoView({block:"nearest",inline:"nearest"});true`)
	chrome.click("[data-panel-tab=library]")
	chrome.waitFor("!document.querySelector('#library').hidden", 5*time.Second)
	chrome.eval(`(()=>{if(document.querySelector('#library').scrollWidth>document.querySelector('#library').clientWidth+1)throw new Error('Library panel overflows');return true})()`)
	t.Log("METRIC studio-library browser_panel=pass viewports=1440,390 preview_insert_save_undo_redo=pass")
}

func TestBrowserStudioLibraryProcessorAllocations(t *testing.T) {
	_, filename := libraryStudio(t)
	wasm, err := os.ReadFile(filepath.Join("..", "..", "build", "cicada-kernel.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	kernelPath, imagePath := filepath.Join(dir, "kernel.wasm"), filepath.Join(dir, "preview.image")
	if err := os.WriteFile(kernelPath, wasm, 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range []studioLibraryItem{{Path: "std/synth", Name: "glassbass", Kind: "instrument"}, {Path: "std/drums", Name: "steel", Kind: "kit"}, {Path: "std/presets", Name: "acid-round", Kind: "preset"}, {Path: "std/fx", Name: "drive", Kind: "fx"}} {
		p, _, err := studioLibraryPreviewProject(filename, item)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := project.CompileEngine(p, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		// Repeat the fixed phrase so all 10,000 measured callbacks render
		// active audio, beyond the UI's bounded one-bar preview duration.
		cfg.LoopSong = true
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(imagePath, image, 0600); err != nil {
			t.Fatal(err)
		}
		for _, profile := range []string{"", "capture"} {
			args := []string{"--expose-gc", "../../host/web/processor_alloc_test.js", kernelPath, imagePath}
			if profile != "" {
				args = append(args, profile)
			}
			output, err := exec.Command("node", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("preview %s profile %q: %v\n%s", item.Name, profile, err, output)
			}
			t.Logf("METRIC studio-preview item=%s rate=48000 worklet_profile=%q %s", item.Name, profile, output)
		}
	}
}
