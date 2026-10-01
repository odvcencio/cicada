package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	webhost "m31labs.dev/cicada/host/web"
)

func TestBrowserCaptureAssetsAndControls(t *testing.T) {
	handler, _ := studioTestHandler(t)
	for _, path := range []string{"/audio/cicada-capture.js", "/audio/cicada-capture-processor.js", "/audio/cicada-capture-worker.js", "/audio/cicada-capture-client.js", "/studio-capture.js"} {
		response := studioCall(t, handler, path, nil)
		if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") || response.Body.Len() == 0 {
			t.Fatalf("asset %s: %d", path, response.Code)
		}
	}
	page := studioCall(t, handler, "/", nil).Body.String()
	for _, id := range []string{"pcm-arm", "pcm-record", "pcm-stop", "pcm-status", "pcm-settings", "pcm-recover", "sampler-root", "sampler-loop", "sampler-play", "sampler-stop"} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Fatalf("missing capture control %s", id)
		}
	}
	if len(webhost.CaptureProcessor())+len(webhost.CaptureAdapter()) > 16*1024 {
		t.Fatal("capture worklet exceeds workstation's 16 KiB raw envelope")
	}
	if !strings.Contains(string(webhost.Client()), "numberOfInputs: 1") {
		t.Fatal("browser worklet input is not exposed")
	}
}

func TestBrowserCaptureFakes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for browser capture contract tests")
	}
	output, err := exec.Command(node, "--test", "../../host/web/capture.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("capture fakes: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}

// Check the browser adapter against lane D's actual Go descriptor and Place,
// rather than relying only on a parallel set of JavaScript assertions.
func TestBrowserBlockUsesCaptureDescriptor(t *testing.T) {
	const script = `require('../../host/web/capture.js');let block;const port={postMessage(p){block=p.timing}};const adapter=new CicadaCapture({port,channels:1,epoch:7},48000,{postMessage(){}});adapter.receive({op:'begin',countInFrames:4});adapter.process([[new Float32Array(8)]],8,100,120000,true);console.log(JSON.stringify(block));`
	output, err := exec.Command("node", "-e", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	var b capture.Block
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.DeviceEpoch != 7 || b.SampleRate != 48000 || b.Frames != 8 || b.Period != 8 || b.Layout != capture.LayoutMono || b.EngineFrame != 96 {
		t.Fatalf("descriptor mismatch: %+v", b)
	}
	p := capture.Place(b)
	if p.Confidence != capture.TimingUnavailable || p.Calibrated || p.EngineFrame != 96 {
		t.Fatalf("browser must not claim calibrated timing: %+v", p)
	}
}
