package kernelimage

import (
	"bytes"
	"encoding/binary"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"testing"
)

func TestArrangementTempoImageRejectsMalformedRecords(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, Patterns: []engine.PatternBank{{}}}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, GatePercent: 55}
	cfg.Schedule = []engine.ScheduleEvent{{Kind: engine.ScheduleTempo, ID: 96000}, {Kind: engine.SchedulePattern, EndTick: 2 * 3840, ID: 1}, {Kind: engine.SchedulePatternEnd, Tick: 2 * 3840, EndTick: 2 * 3840, ID: 1}}
	data, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var record [26]byte
	record[16] = byte(engine.ScheduleTempo)
	binary.LittleEndian.PutUint32(record[22:], 96000)
	at := bytes.Index(data, record[:])
	if at < 0 || bytes.Count(data, record[:]) != 1 {
		t.Fatal("tempo record missing or ambiguous")
	}
	for _, value := range []uint32{0, 19999, 300001, ^uint32(0)} {
		bad := bytes.Clone(data)
		binary.LittleEndian.PutUint32(bad[at+22:], value)
		if _, err := Decode(bad, 48000, 128); err == nil {
			t.Fatal("decoded invalid tempo", value)
		}
	}
	for _, offset := range []int{17, 18, 20} {
		bad := bytes.Clone(data)
		bad[at+offset] = 1
		if _, err := Decode(bad, 48000, 128); err == nil {
			t.Fatal("decoded tempo with placement ownership", offset)
		}
	}
	bad := bytes.Clone(data)
	binary.LittleEndian.PutUint64(bad[at:], 3*3840)
	binary.LittleEndian.PutUint64(bad[at+8:], 3*3840)
	if _, err := Decode(bad, 48000, 128); err == nil {
		t.Fatal("decoded tempo outside placement span")
	}
}
