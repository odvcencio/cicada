package gosxhost

import (
	"errors"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

// recorder collects every batch and decodes it for assertions.
type recorder struct {
	batches [][]byte
	fail    error
}

func (r *recorder) send(batch []byte) error {
	if r.fail != nil {
		return r.fail
	}
	r.batches = append(r.batches, append([]byte(nil), batch...))
	return nil
}

func (r *recorder) commands(t *testing.T, tracks uint8) []cmd.Command {
	t.Helper()
	var out []cmd.Command
	for _, b := range r.batches {
		if len(b)%cmd.CommandSize != 0 {
			t.Fatalf("batch of %d bytes", len(b))
		}
		for at := 0; at < len(b); at += cmd.CommandSize {
			c, err := cmd.DecodeCommand(b[at:at+cmd.CommandSize], tracks)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
	}
	return out
}

func newTestMusic(t *testing.T) (*Music, *recorder) {
	t.Helper()
	rec := &recorder{}
	m, err := NewMusic(testPack(), rec.send)
	if err != nil {
		t.Fatal(err)
	}
	return m, rec
}

func TestStartSendsSetupSceneAndPlayInOneBatch(t *testing.T) {
	m, rec := newTestMusic(t)
	if err := m.Start("explore"); err != nil {
		t.Fatal(err)
	}
	if len(rec.batches) != 1 {
		t.Fatalf("Start posted %d batches, want 1", len(rec.batches))
	}
	cs := rec.commands(t, 2)
	ops := make([]cmd.Op, len(cs))
	for i, c := range cs {
		ops[i] = c.Op
	}
	want := []cmd.Op{cmd.OpDefineMacro, cmd.OpSetState, cmd.OpPlay}
	if len(ops) != len(want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("ops = %v, want %v", ops, want)
		}
	}
	if !m.Started() {
		t.Fatal("not started")
	}
}

func TestSceneLandsOnTheBarTheScoreNames(t *testing.T) {
	m, rec := newTestMusic(t)
	_ = m.Start("explore")
	rec.batches = nil
	if err := m.SetScene("combat"); err != nil {
		t.Fatal(err)
	}
	cs := rec.commands(t, 2)
	if len(cs) != 1 || cs[0].Op != cmd.OpSetState {
		t.Fatalf("commands = %+v", cs)
	}
	c := cs[0]
	// Arg0: scene in the low 16 bits, quantize in the high 16 bits. The kernel
	// applies the explore -> combat rule, which names "bar" (2).
	if c.Index != 1 || c.Arg0&0xffff != 1 {
		t.Fatalf("state/scene = %d/%d, want 1/1", c.Index, c.Arg0&0xffff)
	}
	if c.Tick != 0 {
		t.Fatalf("Tick = %d, want 0 (now; the kernel quantizes)", c.Tick)
	}
	// The observed scene does not change until the kernel reports the landing.
	if m.Scene() != "" {
		t.Fatalf("scene = %q before landing", m.Scene())
	}
	landed := cmd.EncodeMessage(cmd.Message{Kind: cmd.StateChanged, A: 1, B: 1, Tick: 3840})
	if err := m.HandleMessages(landed[:]); err != nil {
		t.Fatal(err)
	}
	if m.Scene() != "combat" {
		t.Fatalf("scene = %q after landing", m.Scene())
	}
}

func TestIntensityIsAMacroTargetInRange(t *testing.T) {
	m, rec := newTestMusic(t)
	if err := m.SetIntensity(0.75); err != nil {
		t.Fatal(err)
	}
	cs := rec.commands(t, 2)
	if len(cs) != 1 || cs[0].Op != cmd.OpSetMacro || cs[0].Index != 0 {
		t.Fatalf("commands = %+v", cs)
	}
	if cs[0].Arg0 != 0x3f400000 { // float32(0.75)
		t.Fatalf("Arg0 = %#x", cs[0].Arg0)
	}
	if cs[0].Arg1 != 19200 {
		t.Fatalf("smoothing = %d frames, want the score's 19200", cs[0].Arg1)
	}
	for _, bad := range []float64{-0.1, 1.1} {
		if err := m.SetIntensity(bad); err == nil {
			t.Errorf("SetIntensity(%v) accepted", bad)
		}
	}
	if err := m.SetMacro("missing", 0.5); err == nil {
		t.Error("unknown macro accepted")
	}
}

func TestUnknownSceneAndStingerSendNothing(t *testing.T) {
	m, rec := newTestMusic(t)
	if err := m.SetScene("nope"); err == nil {
		t.Fatal("unknown scene accepted")
	}
	if err := m.Stinger("nope"); err == nil {
		t.Fatal("unknown stinger accepted")
	}
	if len(rec.batches) != 0 {
		t.Fatalf("sent %d batches for invalid requests", len(rec.batches))
	}
}

func TestStingerAndStop(t *testing.T) {
	m, rec := newTestMusic(t)
	if err := m.Stinger("hit"); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	cs := rec.commands(t, 2)
	if len(cs) != 2 || cs[0].Op != cmd.OpTriggerStinger || cs[0].Track != 1 || cs[1].Op != cmd.OpStop {
		t.Fatalf("commands = %+v", cs)
	}
}

func TestSenderErrorsReachTheCaller(t *testing.T) {
	m, rec := newTestMusic(t)
	rec.fail = errors.New("queue full")
	if err := m.SetScene("combat"); err == nil || err.Error() != "queue full" {
		t.Fatalf("err = %v", err)
	}
}

func TestHandleMessagesTracksBarsFaultsAndLayers(t *testing.T) {
	m, _ := newTestMusic(t)
	var batch []byte
	for _, msg := range []cmd.Message{
		{Kind: cmd.Bar, B: 4, Tick: 3 * 3840},
		{Kind: cmd.LayerChanged, B: 0b11},
		{Kind: cmd.Fault, A: 21},
	} {
		enc := cmd.EncodeMessage(msg)
		batch = append(batch, enc[:]...)
	}
	if err := m.HandleMessages(batch); err != nil {
		t.Fatal(err)
	}
	if m.Bar() != 3 || m.Layers() != 3 {
		t.Fatalf("bar=%d layers=%b", m.Bar(), m.Layers())
	}
	if f := m.Faults(); len(f) != 1 || f[0] != 21 {
		t.Fatalf("faults = %v", f)
	}
	if err := m.HandleMessages(make([]byte, 17)); err == nil {
		t.Fatal("partial message accepted")
	}
}

func TestSetupLargerThanOneBatchIsSplit(t *testing.T) {
	rec := &recorder{}
	p := testPack()
	for i := 0; i < 300; i++ {
		p.Surface.Setup = append(p.Surface.Setup, p.Surface.Setup[0])
	}
	m, err := NewMusic(p, rec.send)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start("explore"); err != nil {
		t.Fatal(err)
	}
	if len(rec.batches) < 2 {
		t.Fatalf("posted %d batches, want a split", len(rec.batches))
	}
	for _, b := range rec.batches {
		if len(b) > batchRecords*cmd.CommandSize {
			t.Fatalf("batch of %d bytes exceeds the bound", len(b))
		}
	}
}
