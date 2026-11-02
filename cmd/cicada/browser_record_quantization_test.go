//go:build browser

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBrowserScoreRecordQuantizationPreviewAndCommit(t *testing.T) {
	if os.Getenv("CICADA_BROWSER") == "windows" {
		t.Skip("synthetic transport setup uses Chrome DevTools")
	}
	for _, tc := range []struct {
		name, division, grid string
		step                 int
		want                 string
	}{
		{"sixteenth", "1/16", "", 1, "{ . c4 . . }"},
		{"eighth", "1/8", "", 2, "{ . . c4 . }"},
		{"eighth-on-triplet", "1/8", "step = 1/8t ", 2, "{ step = 1/8t . . c4 . }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "cicada 2\ntempo 120\ntrack bass acid {}\nlive { record = " + tc.division + " }\npattern take acid steps=4 { " + tc.grid + ". . . . }\nscene main { bass = take }\nsong { main*4 }\n"
			server := startBrowserStudio(t, []byte(source), nil)
			chrome := startBrowserChrome(t, server)
			var preload struct {
				Identifier string `json:"identifier"`
			}
			if err := json.Unmarshal(chrome.mustCall("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": `
window.__gridClock=100;
Object.defineProperty(performance,'now',{value:()=>window.__gridClock});
const RealWS=WebSocket;
window.WebSocket=class { constructor(url){if(!url.includes('/api/transport/ws'))return new RealWS(url);window.__gridSocket=this;} close(){} send(){} };
`}), &preload); err != nil || preload.Identifier == "" {
				t.Fatalf("register synthetic transport: %v", err)
			}
			t.Cleanup(func() {
				chrome.mustCall("Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": preload.Identifier})
			})
			chrome.navigate("http://" + browserStudioAddress + "/")
			chrome.waitFor(`!!window.cicadaPerformance && !!window.__gridSocket && !!document.querySelector('#live-arm-controls input')`, 20*time.Second)
			priorLive := string(chrome.eval(`localStorage.getItem('cicada.live.enabled')`))
			t.Cleanup(func() {
				chrome.eval(`(()=>{const prior=` + priorLive + `;if(prior===null)localStorage.removeItem('cicada.live.enabled');else localStorage.setItem('cicada.live.enabled',prior);return true})()`)
			})
			chrome.eval(`(()=>{
window.cicadaAudio={noteOn:()=>true,noteOff:()=>{},panic:()=>{},params:async()=>({revision:document.body.dataset.revision,registry:[],addresses:[]})};
if(document.getElementById('live-toggle').getAttribute('aria-pressed')!=='true')document.getElementById('live-toggle').click();
const arm=document.querySelector('#live-arm-controls input[value=bass]');arm.checked=true;arm.dispatchEvent(new Event('change',{bubbles:true}));
window.__gridSocket.onmessage({data:JSON.stringify({playing:true,bar:1,step:1,scene:'main',activeSlots:{bass:'take'}})});
document.getElementById('live-record').click();
const n={source:'test',device:'synthetic',channel:0,track:'bass',note:60,velocity:100,time:235.4166667};
window.cicadaPerformance.router.press(n);window.cicadaPerformance.router.release(n);
document.getElementById('live-record').click();return true;
})()`)
			var cells []string
			if err := json.Unmarshal(chrome.eval(`[...document.querySelectorAll('#live-take-preview .take-preview-cell')].map(x=>x.textContent)`), &cells); err != nil {
				t.Fatal(err)
			}
			if len(cells) != 4 || cells[tc.step] != "C4" {
				t.Fatalf("division %s preview: %v", tc.division, cells)
			}
			chrome.eval(`document.getElementById('live-take-commit').click();true`)
			chrome.waitFor(`document.getElementById('live-take-panel').hidden`, 10*time.Second)
			got, err := os.ReadFile(server.score)
			if err != nil || !strings.Contains(string(got), tc.want) {
				t.Fatalf("division %s did not save preview: %s %v", tc.division, got, err)
			}
		})
	}
}
