package takejournal

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/notation"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.cicada")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func writeBlock(t *testing.T, s *Store, id string, raw uint64, values []float32, gap uint64) {
	t.Helper()
	b := capture.RecordedBlock{RawFrame: raw, Timing: capture.Block{SampleRate: 48000, Frames: len(values), GapFrames: gap}, Placement: capture.Placement{EngineFrame: -1}}
	if err := s.Write(id, b, [][]float32{values}); err != nil {
		t.Fatal(err)
	}
}
func TestJournalTornTailAndUnacknowledgedPCM(t *testing.T) {
	s, path := testStore(t)
	id, err := s.Begin("vox", "main", Revision([]byte("score")), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	writeBlock(t, s, id, 0, []float32{0.5, -0.25}, 0)
	if err := s.Flush(id); err != nil {
		t.Fatal(err)
	}
	// Simulate a power loss between PCM flush and the following journal append.
	raw := filepath.Join(filepath.Dir(path), s.rawPath(id))
	f, err := os.OpenFile(raw, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{1, 2, 3, 4})
	f.Close()
	log := filepath.Join(filepath.Dir(path), s.logPath(id))
	f, err = os.OpenFile(log, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"stage":"chunk"`)
	f.Close()
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.Recover(); err != nil {
		t.Fatal(err)
	}
	take, _ := reopened.Get(id)
	if take.Frames != 2 || !take.Incomplete || take.FirstBlock == nil {
		t.Fatalf("recovery=%+v", take)
	}
	wav, err := os.ReadFile(filepath.Join(filepath.Dir(path), take.Asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) != 52 {
		t.Fatalf("unacknowledged audio published: %d", len(wav))
	}
	data, _ := os.ReadFile(log)
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("torn journal tail not repaired")
	}
}
func TestJournalRejectsInteriorCorruptionAndConcurrentOpen(t *testing.T) {
	s, path := testStore(t)
	if other, err := Open(path); err == nil {
		other.Close()
		t.Fatal("second recorder acquired active journal")
	}
	id, _ := s.Begin("vox", "main", Revision(nil), 48000, 1)
	s.Close()
	f, err := os.OpenFile(filepath.Join(filepath.Dir(path), s.logPath(id)), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("corrupt\n")
	f.Close()
	if other, err := Open(path); err == nil {
		other.Close()
		t.Fatal("interior corruption silently discarded")
	}
}
func TestJournalGapPrerollDedupAndImmutableCollision(t *testing.T) {
	s, path := testStore(t)
	var ids []string
	for range 2 {
		id, _ := s.Begin("vox", "main", Revision(nil), 48000, 1)
		writeBlock(t, s, id, 0, []float32{2}, 0)
		writeBlock(t, s, id, 3, []float32{-2}, 2)
		if err := s.Finalize(id, false); err != nil {
			t.Fatal(err)
		}
		if err := s.Publish(id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	ts := s.Takes()
	if ts[0].Asset.SHA256 != ts[1].Asset.SHA256 {
		t.Fatal("same WAV did not deduplicate")
	}
	if !ts[0].Incomplete {
		t.Fatal("gap not marked incomplete")
	}
	blobDir := filepath.Join(filepath.Dir(path), "audio/blobs")
	entries, err := os.ReadDir(blobDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("blob count=%d", len(entries))
	}
	f, err := os.Open(filepath.Join(filepath.Dir(path), ts[0].Asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	h, err := audioasset.ReadWAVHeader(f)
	f.Close()
	if err != nil || h.Encoding != "float" || h.Frames != 4 {
		t.Fatalf("WAV=%+v %v", h, err)
	}
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(path), ts[0].Asset.Path))
	if binary.LittleEndian.Uint32(data[48:52]) != 0 || binary.LittleEndian.Uint32(data[52:56]) != 0 {
		t.Fatal("gap frames not silent")
	}
	source := []byte("cicada 2\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { bass = pulse vox = off // comment\n}\nsong { main }\n")
	selected, err := SelectSource(source, ts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(selected), "start = 1frames") {
		t.Fatal("preroll was not trimmed in clip")
	}
	if !strings.Contains(string(selected), "// comment") {
		t.Fatal("binding comment changed")
	}
	// Change the existing published file: publication must report corruption.
	if err = os.WriteFile(filepath.Join(filepath.Dir(path), ts[0].Asset.Path), []byte("different audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ids[0]); err == nil {
		t.Fatal("corrupt asset silently replaced")
	}
}
func TestJournalRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "audio")); err != nil {
		t.Skip(err)
	}
	path := filepath.Join(dir, "main.cicada")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _ := s.Begin("vox", "main", Revision(nil), 48000, 1)
	writeBlock(t, s, id, 0, []float32{1, 2}, 0)
	if err = s.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(id); err == nil {
		t.Fatal("asset escaped root")
	}
	files, _ := os.ReadDir(outside)
	if len(files) != 0 {
		t.Fatal("wrote outside project")
	}
}
func BenchmarkJournalChunk(b *testing.B) {
	path := filepath.Join(b.TempDir(), "main.cicada")
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	id, err := s.Begin("vox", "main", Revision(nil), 48000, 2)
	if err != nil {
		b.Fatal(err)
	}
	pcm := [][]float32{make([]float32, 256), make([]float32, 256)}
	block := capture.RecordedBlock{Timing: capture.Block{SampleRate: 48000, Frames: 256}}
	b.ReportAllocs()
	b.SetBytes(256 * 8)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		block.RawFrame = uint64(i * 256)
		if err := s.Write(id, block, pcm); err != nil {
			b.Fatal(err)
		}
	}
	if err = s.Flush(id); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	info, err := s.root.Stat(s.logPath(id))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(info.Size())/float64(b.N), "journal-B/block")
}

func TestJournalBatchFlushAndWriterFailure(t *testing.T) {
	s, path := testStore(t)
	id, _ := s.Begin("vox", "main", Revision(nil), 48000, 1)
	writeBlock(t, s, id, 0, []float32{0.75}, 0)
	take, _ := s.Get(id)
	if take.Frames != 0 {
		t.Fatal("unflushed batch was acknowledged")
	}
	if err := s.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	take, _ = s.Get(id)
	if take.Frames != 1 {
		t.Fatal("finalization did not flush short last batch")
	}
	next, _ := s.Begin("vox", "main", Revision(nil), 48000, 1)
	writeBlock(t, s, next, 0, []float32{1}, 0)
	// A failed append poisons the writer: later callbacks must not turn a torn
	// journal suffix into interior corruption or finalize it as a complete take.
	if err := os.Remove(filepath.Join(filepath.Dir(path), s.logPath(next))); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(path), s.logPath(next)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(next); err == nil {
		t.Fatal("storage failure ignored")
	}
	if err := s.Write(next, capture.RecordedBlock{RawFrame: 1, Timing: capture.Block{SampleRate: 48000, Frames: 1}}, [][]float32{{2}}); err == nil {
		t.Fatal("writer continued after storage failure")
	}
	if err := s.Finalize(next, false); err == nil {
		t.Fatal("failed journal finalized")
	}
}

func TestJournalRecoverChecksLiveTail(t *testing.T) {
	for _, tail := range []string{`{"stage":"finalized"`, "corrupt\n"} {
		t.Run(tail, func(t *testing.T) {
			s, path := testStore(t)
			id, err := s.Begin("vox", "main", Revision(nil), 48000, 1)
			if err != nil {
				t.Fatal(err)
			}
			writeBlock(t, s, id, 0, []float32{.75}, 0)
			if err := s.Flush(id); err != nil {
				t.Fatal(err)
			}
			f, err := s.root.OpenFile(s.logPath(id), os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(tail); err != nil {
				t.Fatal(err)
			}
			f.Close()
			err = s.Recover()
			if strings.HasSuffix(tail, "\n") {
				if err == nil {
					t.Fatal("Recover ignored journal corruption")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			take, err := reopened.Get(id)
			if err != nil || take.Frames != 1 {
				t.Fatalf("take=%+v %v", take, err)
			}
		})
	}
}

func TestSourceTrimsInitialGapAcrossCountIn(t *testing.T) {
	s, _ := testStore(t)
	id, err := s.Begin("vox", "main", Revision(nil), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	timing := capture.Block{SampleRate: 48000, Frames: 4, GapFrames: 6, EngineFrame: 2}
	first := capture.RecordedBlock{RawFrame: 6, Timing: timing, Placement: capture.Place(timing)}
	if err := s.Write(id, first, [][]float32{{.25, .5, .75, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Finalize(id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, _ := s.Get(id)
	selected, err := SelectSource([]byte("cicada 2\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { vox = off bass = pulse }\nsong { main }\n"), take)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(selected), "start = 4frames") {
		t.Fatalf("count-in silence was not trimmed across the initial gap: %s", selected)
	}
	if !strings.Contains(string(selected), "end = 10frames") {
		t.Fatal("initial loss duration was closed")
	}
}

func TestSelectSourceOnAudioPresetTrack(t *testing.T) {
	s, _ := testStore(t)
	id, err := s.Begin("vox", "main", Revision(nil), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	writeBlock(t, s, id, 0, []float32{.25, .5, .75, 1}, 0)
	if err := s.Finalize(id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("cicada 2\npreset captured { instrument=audio level=-9dB }\ntrack vox captured {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { vox=off bass=pulse }\nsong { main }\n")
	selected, err := SelectSource(source, take)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(selected, []byte("preset captured { instrument=audio level=-9dB }")) || !bytes.Contains(selected, []byte("track vox captured {}")) || !bytes.Contains(selected, []byte("vox="+id+"-clip")) {
		t.Fatalf("preset or scene binding changed incorrectly: %s", selected)
	}
	if score, ds := notation.ParseEdition(selected, 2); score == nil || len(ds) != 0 {
		t.Fatalf("selected source invalid: %+v", ds)
	}
}
