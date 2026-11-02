//go:build browser

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBrowserTripletTakePreviewAndCommit(t *testing.T) {
	if os.Getenv("CICADA_BROWSER") == "windows" {
		t.Skip("synthetic transport setup uses Chrome DevTools")
	}
	for _, stale := range []bool{false, true} {
		name := "commit"
		if stale {
			name = "refreshed-project-conflict"
		}
		t.Run(name, func(t *testing.T) {
			source := "cicada 2\ntitle \"Triplet take\"\ntempo 120\ntrack bass acid {}\npattern trip acid steps=4 { step = 1/8t 1 . 5 . }\nscene main { bass = trip }\nsong { main*4 }\n"
			server := startBrowserStudio(t, []byte(source), nil)
			chrome := startBrowserChrome(t, server)
			chrome.setViewport(1440, 1000)
			chrome.mustCall("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": `
window.__gridClock=100;
Object.defineProperty(performance,'now',{value:()=>window.__gridClock});
const RealWS=WebSocket;
window.WebSocket=class { constructor(url){if(!url.includes('/api/transport/ws'))return new RealWS(url);window.__gridSocket=this;} close(){} send(){} };
`})
			chrome.navigate("http://" + browserStudioAddress + "/")
			chrome.waitFor(`!!window.cicadaPerformance && !!window.__gridSocket && !!document.querySelector('#live-arm-controls input')`, 20*time.Second)
			chrome.eval(`(()=>{
window.cicadaAudio={noteOn:()=>true,noteOff:()=>{},panic:()=>{},params:async()=>({revision:document.body.dataset.revision,registry:[],addresses:[]})};
document.getElementById('live-toggle').click();
const arm=document.querySelector('#live-arm-controls input[value=bass]');arm.checked=true;arm.dispatchEvent(new Event('change',{bubbles:true}));
window.__gridSocket.onmessage({data:JSON.stringify({playing:true,bar:1,step:1,scene:'main',activeSlots:{bass:'trip'}})});
document.getElementById('live-record').click();
const n={source:'test',device:'synthetic',channel:0,track:'bass',note:60,velocity:100,time:172.9166667};
window.cicadaPerformance.router.press(n);window.cicadaPerformance.router.release(n);
document.getElementById('live-record').click();return true;
})()`)
			var result struct {
				Grid   string   `json:"grid"`
				Cells  []string `json:"cells"`
				Active []bool   `json:"active"`
			}
			if err := json.Unmarshal(chrome.eval(`(()=>{const cells=[...document.querySelectorAll('#live-take-preview .take-preview-cell')];return {grid:document.querySelector('.pattern').dataset.stepTicks,cells:cells.map(x=>x.textContent),active:cells.map(x=>x.classList.contains('has-note'))}})()`), &result); err != nil {
				t.Fatal(err)
			}
			if result.Grid != "320" || len(result.Cells) != 4 || result.Cells[0] != "C4" || len(result.Active) != 4 || !result.Active[0] || result.Active[1] || result.Active[2] || result.Active[3] {
				t.Fatalf("triplet take preview: %+v", result)
			}
			if stale {
				external := source + "\n// external edit\n"
				if err := os.WriteFile(server.score, []byte(external), 0600); err != nil {
					t.Fatal(err)
				}
				chrome.eval(`(async()=>{const html=await (await fetch('/')).text();const next=new DOMParser().parseFromString(html,'text/html');document.body.dataset.revision=next.body.dataset.revision;document.body.dataset.sourceRevision=next.body.dataset.sourceRevision;return true})()`)
				chrome.eval(`document.getElementById('live-take-commit').click();true`)
				chrome.waitFor(`document.getElementById('studio-status').dataset.state === 'error'`, 10*time.Second)
				if string(chrome.eval(`document.getElementById('live-take-panel').hidden`)) != "false" {
					t.Fatal("conflict discarded the recorded take")
				}
				if got, err := os.ReadFile(server.score); err != nil || string(got) != external {
					t.Fatalf("conflict changed the outside edit: %q %v", got, err)
				}
				return
			}
			chrome.eval(`document.getElementById('live-take-commit').click();true`)
			chrome.waitFor(`document.getElementById('live-take-panel').hidden`, 10*time.Second)
			got, err := os.ReadFile(server.score)
			if err != nil || !strings.Contains(string(got), "step = 1/8t c4 . 5 .") {
				t.Fatalf("saved take did not match preview: %s %v", got, err)
			}
		})
	}
}
