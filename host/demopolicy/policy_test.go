package demopolicy

import (
	"errors"
	"testing"
)

func TestDemoRejectsEveryExportBeforeSideEffects(t *testing.T) {
	for _, input := range []string{
		string(ExportAudio), string(ExportMIDI), string(ExportProject), string(ExportStems), string(ExportJob),
		"render", "download", "saveAs", "Export.audio", " export.audio", "export.audio\x00", "agent.execute", "webmcp.export",
		string(NativeAudio), string(ReadLocalFile), string(WriteLocalFile),
	} {
		t.Run(input, func(t *testing.T) {
			called := false
			err := PublicDemo().Dispatch(Action(input), func() error { called = true; return nil })
			if !errors.Is(err, ErrDenied) || called {
				t.Fatalf("command %q bypassed demo policy: called=%v error=%v", input, called, err)
			}
		})
	}
}

func TestProfilesPreserveOSSAndDenyUnsetPolicy(t *testing.T) {
	for _, action := range []Action{Play, Stop, Note, Parameter, Edit, Undo, Redo, Reset, Capture, MIDIInput} {
		called := 0
		if err := PublicDemo().Dispatch(action, func() error { called++; return nil }); err != nil || called != 1 {
			t.Fatalf("%s: called=%d error=%v", action, called, err)
		}
	}
	for _, action := range []Action{ExportAudio, ExportMIDI, ExportProject, ExportStems, ExportJob, NativeAudio, ReadLocalFile, WriteLocalFile} {
		if !LocalOSS().Allows(action) {
			t.Fatalf("local OSS lost %s", action)
		}
	}
	var unset Policy
	if unset.Allows(Play) || !errors.Is(unset.Dispatch(Play, func() error { t.Fatal("zero policy invoked handler"); return nil }), ErrDenied) {
		t.Fatal("zero policy must fail closed")
	}
}
