package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/project"
)

func browserTakeFixture(t *testing.T) *studioBrowserTake {
	t.Helper()
	// Use F's actual adapter and writer, rather than inventing their wire data.
	const script = `
require('../../host/web/capture.js');
const {CaptureWriter}=require('../../host/web/capture-worker.js');
(async()=>{
  const blocks=[],chunks=[];
  let offset=0,queue=Promise.resolve();
  const store={metadata:{channels:2,sampleRate:48000},async append(record,bytes){record.offset=offset;record.length=bytes.length;offset+=bytes.length;blocks.push(record);chunks.push(Buffer.from(bytes));},async finish(){}};
  const writer=new CaptureWriter(store);
  const port={postMessage(packet){queue=queue.then(()=>writer.append(packet));}};
  const adapter=new CicadaCapture({port,channels:2,epoch:7},48000,{postMessage(){}});
  adapter.receive({op:'begin',countInFrames:2});
  adapter.process([[new Float32Array([0,.125,.25,.375]),new Float32Array([0,-.125,-.25,-.375])]],4,0,120000,true);
  await queue;
  adapter.gap=2;
  adapter.process([[new Float32Array([.5,.625,.75,.875]),new Float32Array([-.5,-.625,-.75,-.875])]],4,2,120000,true);
  await queue;
  const summary=await writer.finish(3);
  console.log(JSON.stringify({sampleRate:48000,channels:2,pcm:Buffer.concat(chunks).toString('base64'),blocks:blocks.map(({timing,rawFrame,offset,length})=>({timing,rawFrame,offset,length})),rawFrames:summary.rawFrames,incomplete:summary.incomplete}));
})().catch(error=>{console.error(error);process.exitCode=1});`
	output, err := exec.Command("node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("browser capture fixture: %v\n%s", err, output)
	}
	var take studioBrowserTake
	if err := json.Unmarshal(output, &take); err != nil {
		t.Fatal(err)
	}
	return &take
}

func assertTakeSampler(t *testing.T, s *studio, id string, left, right []float32) {
	t.Helper()
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		t.Fatal(err)
	}
	var asset project.Asset
	var clip project.Clip
	for _, a := range p.Assets {
		if a.Name == id {
			asset = a
		}
	}
	for _, c := range p.Clips {
		if c.Name == id+"-clip" {
			clip = c
		}
	}
	if clip.Asset != asset.Name || asset.Name == "" || asset.Source != "recorded" || p.Scenes[0].Bindings["vox"] != clip.Name {
		t.Fatalf("published asset/clip is not selected: %+v / %+v", asset, clip)
	}
	region, err := sampleasset.LoadRegion(takeRoot(s.path), asset, clip.StartFrame, clip.EndFrame, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := sample.New(48000, region)
	if err != nil {
		t.Fatal(err)
	}
	if err := voice.NoteOn(60, 127); err != nil {
		t.Fatal(err)
	}
	gotLeft, gotRight := make([]float32, len(left)), make([]float32, len(right))
	voice.Render(gotLeft, gotRight)
	for i := range left {
		if math.Float32bits(gotLeft[i]) != math.Float32bits(left[i]) || math.Float32bits(gotRight[i]) != math.Float32bits(right[i]) {
			t.Fatalf("sampler frame %d: %v/%v != captured %v/%v", i, gotLeft[i], gotRight[i], left[i], right[i])
		}
	}
	response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "audition", Revision: studioRevision(source), TakeID: id, Sample: &studioSampleRequest{Root: 60, Note: 60}})
	if response.Code != http.StatusOK {
		t.Fatalf("published audition: %d %s", response.Code, response.Body)
	}
	wav := response.Body.Bytes()
	if len(wav) < 44+len(left)*8 || response.Header().Get("Content-Type") != "audio/wav" {
		t.Fatal("audition did not return float32 WAV")
	}
	for i := range left {
		if binary.LittleEndian.Uint32(wav[44+i*8:]) != math.Float32bits(left[i]) || binary.LittleEndian.Uint32(wav[48+i*8:]) != math.Float32bits(right[i]) {
			t.Fatalf("audition changed captured PCM at frame %d", i)
		}
	}
	if p.Tracks[0].Kind != "audio" || p.Scenes[0].Bindings["vox"] != clip.Name {
		t.Fatal("sampler preparation mutated the project")
	}
	t.Logf("METRIC capture_journal_asset_sampler_frames=%d channels=2 max_sample_difference=0", len(left))
}

func TestCapturedTakePublishesAndRendersExactSamples(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		s := newTakeStudio(t, t.TempDir())
		id := captureTestTake(t, s)
		if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); err != nil {
			t.Fatal(err)
		}
		left, right := make([]float32, 48), make([]float32, 48)
		for i := range left {
			left[i], right[i] = float32(i)/48, -float32(i)/24
		}
		assertTakeSampler(t, s, id, left, right)
		source, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		// Exercise A's parsed sampler table, including its voice count and root.
		source = append(source, []byte(fmt.Sprintf("\nsampler recorded { asset = %s root = c4 mode = oneshot voices = 2 }\n", id))...)
		p, err := compileStudioSource(s.path, source)
		if err != nil {
			t.Fatal(err)
		}
		pool, err := sampleasset.LoadSampler(takeRoot(s.path), p, "recorded")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.NoteOn(60, 127); err != nil {
			t.Fatal(err)
		}
		gotLeft, gotRight := make([]float32, 48), make([]float32, 48)
		pool.Render(gotLeft, gotRight)
		for i := range left {
			if gotLeft[i] != left[i] || gotRight[i] != right[i] {
				t.Fatalf("declared sampler changed recorded frame %d", i)
			}
		}
	})
	t.Run("browser-preroll-and-gaps", func(t *testing.T) {
		s := newTakeStudio(t, t.TempDir())
		response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "import", Revision: studioRevision([]byte(audioTakeScore)), Track: "vox", Scene: "main", Capture: browserTakeFixture(t)})
		if response.Code != http.StatusOK {
			t.Fatalf("browser publication: %d %s", response.Code, response.Body)
		}
		var result struct{ Take string }
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		left := []float32{.25, .375, 0, 0, .5, .625, .75, .875, 0, 0, 0}
		right := []float32{-.25, -.375, 0, 0, -.5, -.625, -.75, -.875, 0, 0, 0}
		assertTakeSampler(t, s, result.Take, left, right)
		take, err := s.takes.Get(result.Take)
		if err != nil || take.Frames != 13 || !take.Incomplete || take.FirstBlock.Placement.EngineFrame != -2 {
			t.Fatalf("raw duration, loss or preroll lost: %+v, %v", take, err)
		}
	})
}

func TestBrowserTakeConflictRetainsPublishedAudio(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	current := []byte(audioTakeScore + "// concurrent source edit\n")
	if err := os.WriteFile(s.path, current, 0600); err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "import", Revision: studioRevision([]byte(audioTakeScore)), Track: "vox", Scene: "main", Capture: browserTakeFixture(t)})
	if response.Code != http.StatusConflict {
		t.Fatalf("revision race: %d %s", response.Code, response.Body)
	}
	takes := s.takes.Takes()
	if len(takes) != 1 || takes[0].Stage != takejournal.Conflict || takes[0].Frames != 13 {
		t.Fatalf("conflicted audio lost: %+v", takes)
	}
	response = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "recover", Revision: studioRevision(current), TakeID: takes[0].ID})
	if response.Code != http.StatusOK {
		t.Fatalf("explicit recovery: %d %s", response.Code, response.Body)
	}
}

func TestAudioScoreCaptureBackingPreservesProjectAndIndices(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	before, err := project.CanonicalJSON(s.lastGoodProject)
	if err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, s.routes(), "/api/kernel-image?rate=48000&capture=1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("capture accompaniment: %d %s", response.Code, response.Body)
	}
	audio, _ := simulatedCaptureAudio(256, zeroAudioSource{})
	s.transport.audio, s.transport.sampleRate = audio, 48000
	s.transport.audioOptions.InputEnabled = true
	r, err := capture.NewRecorder(4, 256, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transport.armCapture(r); err != nil {
		t.Fatal(err)
	}
	if err := s.transport.startCapture(capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	if index, ok := s.transport.stream.TrackIndex("vox"); !ok || index != 0 {
		t.Fatal("capture changed track indices")
	}
	s.transport.stop()
	if err := s.transport.disarmCapture(); err != nil {
		t.Fatal(err)
	}
	after, err := project.CanonicalJSON(s.lastGoodProject)
	if err != nil || string(before) != string(after) {
		t.Fatal("capture backing changed the project")
	}
}

func TestAudioOnlyCaptureBacking(t *testing.T) {
	for _, host := range []string{"native", "browser"} {
		t.Run(host, func(t *testing.T) {
			s := newTakeStudio(t, t.TempDir())
			id := captureTestTake(t, s)
			if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			text := strings.ReplaceAll(string(source), "track bass acid {}\n", "")
			text = strings.ReplaceAll(text, "pattern pulse acid steps=4 { 1 . 5 . }\n", "")
			text = strings.ReplaceAll(text, "bass = pulse ", "")
			if err := os.WriteFile(s.path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := compileStudioSource(s.path, []byte(text))
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Patterns) != 0 || len(p.Clips) == 0 {
				t.Fatal("fixture is not audio only")
			}
			before, err := project.CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			if host == "browser" {
				response := studioCall(t, s.routes(), "/api/kernel-image?rate=48000&capture=1", nil)
				if response.Code != http.StatusOK {
					t.Fatalf("browser capture preparation: %d %s", response.Code, response.Body)
				}
			} else {
				audio, _ := simulatedCaptureAudio(256, zeroAudioSource{})
				s.transport.audio, s.transport.sampleRate = audio, 48000
				s.transport.audioOptions.InputEnabled = true
				r, err := capture.NewRecorder(4, 256, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				if err := s.transport.armCapture(r); err != nil {
					t.Fatal(err)
				}
				if err := s.transport.startCapture(capture.Calibration{}); err != nil {
					t.Fatal(err)
				}
				if index, ok := s.transport.stream.TrackIndex("vox"); !ok || index != 0 {
					t.Fatal("capture changed track index")
				}
				s.transport.stop()
				if err := s.transport.disarmCapture(); err != nil {
					t.Fatal(err)
				}
			}
			backing := captureBacking(p)
			if err := project.ValidateProject(backing); err != nil {
				t.Fatal(err)
			}
			if backing.Scenes[0].Bindings["vox"] != "off" {
				t.Fatal("audio accompaniment is not silent")
			}
			after, err := project.CanonicalJSON(p)
			if err != nil || string(before) != string(after) {
				t.Fatal("capture changed original project")
			}
		})
	}
}
