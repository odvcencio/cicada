//go:build browser

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBrowserMasterChain(t *testing.T) {
	source := acceptedMasterScore(t)
	// Exercise multiple stages and preserve a nonalphabetic authored order.
	source = []byte(strings.Replace(string(source), "master { insert = glue }", "fx stereo width { amount = 1.08 bass_mono = 100Hz }\nmaster { insert = stereo -> glue }", 1))
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.click(`[data-panel-tab="master"]`)
	chrome.waitFor(`!document.getElementById('mixer-settings').hidden`, 5*time.Second)
	chrome.click(`.master-mixer-link`)
	chrome.waitFor(`document.getElementById('mixer-settings').getBoundingClientRect().top < 300`, 5*time.Second)
	result := chrome.eval(`(()=>({names:[...document.querySelectorAll('.master-chain h3')].map(x=>x.textContent),target:document.getElementById('mixer-settings').textContent,overflow:document.documentElement.scrollWidth>window.innerWidth}))()`)
	var state struct {
		Names    []string
		Target   string
		Overflow bool
	}
	if err := json.Unmarshal(result, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Names) != 3 || state.Names[0] != "stereo · width" || state.Names[1] != "glue · comp" || state.Names[2] != "Safety limiter" {
		t.Fatalf("chain order: %+v", state)
	}
	if !strings.Contains(state.Target, "-14 LUFS") || !strings.Contains(state.Target, "-1 dBTP") || state.Overflow {
		t.Fatalf("desktop target/fit: %+v", state)
	}
	chrome.screenshot("master-chain-desktop.png")
	chrome.setViewport(390, 900)
	chrome.eval(`document.getElementById('mixer-settings').scrollIntoView({block:'start'});true`)
	chrome.waitFor(`window.innerWidth===390`, 5*time.Second)
	result = chrome.eval(`(()=>({overflow:document.documentElement.scrollWidth>window.innerWidth,visible:[...document.querySelectorAll('.master-chain h3')].every(x=>{const r=x.getBoundingClientRect();return r.left>=0&&r.right<=window.innerWidth})}))()`)
	var mobile struct{ Overflow, Visible bool }
	if err := json.Unmarshal(result, &mobile); err != nil {
		t.Fatal(err)
	}
	if mobile.Overflow || !mobile.Visible {
		t.Fatalf("mobile chain fit: %+v", mobile)
	}
	chrome.screenshot("master-chain-mobile.png")
}
