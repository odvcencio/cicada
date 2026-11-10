package enginebuild

import "testing"

func TestWorkingLimitStaysUnderTheCeiling(t *testing.T) {
	if MaxBytes > Ceiling {
		t.Fatalf("MaxBytes %d is above the %d-byte ceiling", MaxBytes, Ceiling)
	}
	if MaxBytes%65536 != 0 {
		t.Fatalf("MaxBytes %d is not a multiple of 65,536", MaxBytes)
	}
}

func TestCommandBuildsTheEngineForTheBrowser(t *testing.T) {
	command := Command("out.wasm", ReleaseLDFlags, "-overlay", "o.json")
	want := []string{"go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", "out.wasm", "-overlay", "o.json", "./engine"}
	if len(command.Args) != len(want) {
		t.Fatalf("args = %q", command.Args)
	}
	for i := range want {
		if command.Args[i] != want[i] {
			t.Fatalf("args = %q, want %q", command.Args, want)
		}
	}
	env := map[string]bool{}
	for _, e := range command.Env {
		env[e] = true
	}
	for _, e := range []string{"GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off"} {
		if !env[e] {
			t.Errorf("environment lacks %s", e)
		}
	}
}
