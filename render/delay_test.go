package render

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestDelaySendChangesWAVDeterministically(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx", "delay-send.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	render := func(source []byte) ([]byte, Report) {
		t.Helper()
		score, diagnostics := notation.Parse(source)
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("source: %+v", diagnostic)
			}
		}
		var output bytes.Buffer
		report, err := WAV(score, Options{SampleRate: 48_000, Bars: 1, TailSec: 1}, &output)
		if err != nil {
			t.Fatal(err)
		}
		if report.writtenFrames != report.Frames || len(output.Bytes()) != 68+int(report.Frames)*6 {
			t.Fatalf("delay changed WAV duration: %+v, bytes=%d", report, output.Len())
		}
		return output.Bytes(), report
	}
	wet, report := render(source)
	again, _ := render(source)
	if !bytes.Equal(wet, again) {
		t.Fatal("delay render is not deterministic")
	}
	dry, _ := render(bytes.Replace(source, []byte("send_a = 0.4"), []byte("send_a = 0"), 1))
	changed := 0
	for index := 68; index+6 <= len(wet); index += 6 {
		if !bytes.Equal(wet[index:index+6], dry[index:index+6]) {
			changed++
		}
	}
	if changed < 100 || report.OutputPeak == 0 || report.ClippedSamples != 0 {
		t.Fatalf("delay return was silent or clipped: changed=%d report=%+v", changed, report)
	}
}

func TestDelayPreAndPostFaderRouting(t *testing.T) {
	render := func(levelDB int, pre, send bool) []byte {
		t.Helper()
		sendText := ""
		if send {
			sendText = " send_a = 1"
			if pre {
				sendText += " send_pre = true"
			}
		}
		source := "cicada 1\ntempo 120\nkey a minor\nfx delay { time = 250ms feedback = 0 mix = 1 }\n" +
			"track bass acid { level = " + strconv.Itoa(levelDB) + "db" + sendText + " }\n" +
			"pattern riff acid steps=16 { 1 . . . | . . . . | . . . . | . . . . }\nscene main { bass=riff }\nsong { main }\n"
		score, diagnostics := notation.Parse([]byte(source))
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("source: %+v", diagnostic)
			}
		}
		var output bytes.Buffer
		if _, err := WAV(score, Options{SampleRate: 48_000, TailSec: .5}, &output); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	energy := func(wet, dry []byte) float64 {
		t.Helper()
		var power float64
		for frame := 12_000; frame < 24_000; frame++ {
			index := 68 + frame*6
			a := int32(wet[index]) | int32(wet[index+1])<<8 | int32(wet[index+2])<<16
			b := int32(dry[index]) | int32(dry[index+1])<<8 | int32(dry[index+2])<<16
			if a&0x800000 != 0 {
				a |= ^int32(0xffffff)
			}
			if b&0x800000 != 0 {
				b |= ^int32(0xffffff)
			}
			diff := float64(a - b)
			power += diff * diff
		}
		return math.Sqrt(power / 12_000)
	}
	dry6, dry18 := render(-6, false, false), render(-18, false, false)
	pre6, pre18 := energy(render(-6, true, true), dry6), energy(render(-18, true, true), dry18)
	post6, post18 := energy(render(-6, false, true), dry6), energy(render(-18, false, true), dry18)
	if pre6 < 100 || post6 < 100 {
		t.Fatalf("delay return too quiet to compare: pre=%g post=%g", pre6, post6)
	}
	if ratio := pre18 / pre6; ratio < .98 || ratio > 1.02 {
		t.Fatalf("pre-fader send followed the fader: ratio %g", ratio)
	}
	if ratio := post18 / post6; ratio < .24 || ratio > .27 {
		t.Fatalf("post-fader send ignored the fader: ratio %g", ratio)
	}
}
