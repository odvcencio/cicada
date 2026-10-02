//go:build browser

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// This gate must run in genuine Chrome. Native HTTP and Node projection checks
// do not establish that the integrated grid can edit and stage a v15 image.
func TestBrowserChordGridIntegration(t *testing.T) {
	source := strings.Replace(studioChordScore, "[d4 f4 a4]", "[d4 f4 a4 c5]", 1)
	server := startBrowserStudio(t, []byte(source), nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.waitFor(`!!document.querySelector('.pitch-cell[data-pattern="harmony"][data-step="0"][data-pitch="65"]')`, 5*time.Second)
	for _, pitch := range []int{62, 65, 69, 72} {
		selector := fmt.Sprintf(`.pitch-cell[data-pattern="harmony"][data-step="0"][data-pitch="%d"]`, pitch)
		chrome.waitFor(`(()=>{const e=document.querySelector(`+strconvQuote(selector)+`);return !!e&&e.classList.contains('on')&&e.getAttribute('aria-label').startsWith('Remove ')&&e.getClientRects().length>0})()`, 5*time.Second)
	}
	chrome.screenshot("chord-grid-integrated-desktop.png")
	chrome.eval(`(()=>{const select=document.getElementById('audio-mode');select.value='browser';select.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
	chrome.waitFor(`!document.getElementById('start-audio').hidden`, 5*time.Second)
	chrome.click("#start-audio")
	chrome.waitFor(`window.cicadaBrowserAudio.context?.state==='running'&&document.getElementById('studio-status').textContent.includes('Browser audio ready')`, 20*time.Second)

	readSource := func() string {
		t.Helper()
		data, err := os.ReadFile(server.score)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	assertSource := func(want string) {
		t.Helper()
		if got := readSource(); got != want {
			t.Fatalf("browser grid changed unrelated source bytes\ngot: %s\nwant: %s", got, want)
		}
		chordGridProject(t, []byte(want))
	}
	stageSettled := func() {
		t.Helper()
		chrome.waitFor(`!document.getElementById('save-source').disabled&&document.body.dataset.revision===window.cicadaBrowserAudio.revision`, 20*time.Second)
	}
	edit := func(selector string) {
		t.Helper()
		chrome.eval(`window.__gridPriorRevision=document.body.dataset.revision;true`)
		chrome.click(selector)
		chrome.waitFor(`document.body.dataset.revision!==window.__gridPriorRevision&&!document.getElementById('save-source').disabled`, 20*time.Second)
		stageSettled()
	}
	history := func(action string) {
		t.Helper()
		chrome.eval(`window.__gridPriorRevision=document.body.dataset.revision;document.querySelector('[data-panel-tab="history"]').click();true`)
		chrome.waitFor(`!document.getElementById('history-`+action+`').disabled`, 5*time.Second)
		chrome.click("#history-" + action)
		chrome.waitFor(`document.body.dataset.revision!==window.__gridPriorRevision`, 20*time.Second)
		stageSettled()
		chrome.eval(`document.querySelector('[data-panel-tab="session"]').click();true`)
	}

	// A fifth pitch must leave both the file and revision unchanged and expose
	// the actionable toolbar error on the current, integrated page.
	chrome.eval(`window.__gridInitialRevision=document.body.dataset.revision;true`)
	chrome.click(`.pitch-cell[data-pattern="harmony"][data-step="0"][data-pitch="63"]`)
	chrome.waitFor(`(()=>{const error=document.getElementById('bar-error');return !document.getElementById('save-source').disabled&&!error.hidden&&getComputedStyle(error).display!=='none'&&error.title.includes('remove a pitch before adding another')})()`, 10*time.Second)
	chrome.waitFor(`document.body.dataset.revision===window.__gridInitialRevision`, 5*time.Second)
	assertSource(source)
	chrome.screenshot("chord-grid-integrated-fifth-pitch-error.png")

	// Removing a middle pitch keeps every other pitch, modifier, tie/rest and
	// the exact focused pitch identity when the projection is replaced.
	selector := `.pitch-cell[data-pattern="harmony"][data-step="0"][data-pitch="65"]`
	chrome.eval(`document.querySelector(` + strconvQuote(selector) + `).focus();true`)
	edit(selector)
	edited := strings.Replace(source, "[d4 f4 a4 c5]", "[d4 a4 c5]", 1)
	assertSource(edited)
	chrome.waitFor(`(()=>{const e=document.activeElement;return e?.dataset.pattern==='harmony'&&e.dataset.step==='0'&&e.dataset.pitch==='65'&&!e.classList.contains('on')&&e.tabIndex===0&&document.getElementById('bar-error').hidden})()`, 5*time.Second)
	chrome.waitFor(`document.querySelectorAll('details.pattern .step-edit[tabindex="0"]').length===1`, 5*time.Second)

	history("undo")
	assertSource(source)
	history("redo")
	assertSource(edited)

	// Adding the same pitch appends that spelling without disturbing existing
	// authored order. Undo restores the exact prior source, not a respelling.
	edit(selector)
	added := strings.Replace(edited, "[d4 a4 c5]", "[d4 a4 c5 f4]", 1)
	assertSource(added)
	history("undo")
	assertSource(edited)

	edit(`.note.step-edit[data-pattern="harmony"][data-step="0"]`)
	assertSource(strings.Replace(edited, "[d4 a4 c5]^?70", ".", 1))
	history("undo")
	assertSource(edited)

	chrome.setViewportMode(390, 900, false)
	chrome.waitFor(`window.innerWidth===390`, 5*time.Second)
	chrome.screenshot("chord-grid-integrated-mobile.png")
	chrome.mustCall("Page.reload", map[string]any{"ignoreCache": true})
	chrome.waitFor(`document.readyState==='complete'&&document.querySelector('.note.step-edit[data-pattern="harmony"][data-step="0"]')?.textContent.includes('C5')`, 10*time.Second)
	assertSource(edited)
	t.Log("genuine browser integrated chord grid: four pitches, atomic fifth-pitch refusal, exact source/focus, add/remove, undo/redo, whole-chord clear, v15 staging, narrow viewport and persistence")
}
