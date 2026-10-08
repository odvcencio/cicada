//go:build browser

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserDraftConflict(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.eval(`(()=>{const mode=document.getElementById('audio-mode');mode.value='browser';mode.dispatchEvent(new Event('change',{bubbles:true}));window.__realFetch=window.fetch.bind(window);window.fetch=async(url,options)=>{if(url==='/api/state'&&window.__savedRevision&&!window.__heldState){window.__heldState=true;await new Promise(resolve=>window.__releaseState=resolve)}const response=await window.__realFetch(url,options);if(url==='/api/source'&&response.ok)window.__savedRevision=(await response.clone().json()).revision;return response};document.getElementById('edit-source').click();const editor=document.getElementById('source-editor');editor.value=editor.value.replace('First acid','Saved browser score');editor.dispatchEvent(new Event('input',{bubbles:true}));document.getElementById('save-source').click();return true})()`)
	chrome.waitFor("!!window.__releaseState", 10*time.Second)
	chrome.eval(`(()=>{const editor=document.getElementById('source-editor');editor.value=editor.value.replace('Saved browser score','Open browser draft');editor.dispatchEvent(new Event('input',{bubbles:true}));window.__draft=editor.value;return true})()`)
	external := strings.Replace(string(source), "First acid", "External score edit", 1)
	if err := os.WriteFile(server.score, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	chrome.waitFor(`(async()=>{const state=await (await window.__realFetch('/api/state')).json();return state.source.includes('External score edit')&&state.revision!==window.__savedRevision})()`, 10*time.Second)
	chrome.eval("window.__releaseState();true")
	chrome.waitFor("!document.getElementById('save-source').disabled", 10*time.Second)
	var preserved bool
	if err := json.Unmarshal(chrome.eval(`document.body.dataset.revision===window.__savedRevision&&document.getElementById('source-editor').value===window.__draft`), &preserved); err != nil || !preserved {
		t.Fatalf("browser save advanced the open draft revision or replaced its text: %s", chrome.eval(`({revision:document.body.dataset.revision,savedRevision:window.__savedRevision,source:document.getElementById('source-editor').value})`))
	}
	chrome.eval("document.getElementById('save-source').click();true")
	chrome.waitFor("!document.getElementById('save-source').disabled&&document.getElementById('studio-status').dataset.state==='error'", 10*time.Second)
	if content, err := os.ReadFile(server.score); err != nil || string(content) != external {
		t.Fatalf("conflicting browser draft overwrote the external edit: %v", err)
	}
	if string(chrome.eval("document.getElementById('source-editor').value===window.__draft")) != "true" {
		t.Fatal("conflict discarded the browser draft")
	}
}
