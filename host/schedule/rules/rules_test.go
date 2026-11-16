package rules_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/schedule/rules"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// The rules run where the score is held in memory, such as a browser tab, so
// neither they nor the project compiler may link the host's network or audio
// file packages.
func TestRulesLinkNoHostAudioOrNetwork(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	cmd := exec.Command("go", "list", "-deps", "m31labs.dev/cicada/host/schedule/rules", "m31labs.dev/cicada/project")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch dep {
		case "net/http", "m31labs.dev/cicada/host/instrumentpack", "m31labs.dev/cicada/host/sampleasset":
			t.Errorf("the schedule rules depend on %s", dep)
		}
	}
}

// packs records the packs a check asks for and answers with err.
type packs struct {
	asked []string
	err   error
}

func (p *packs) Check(path, pin string) error {
	p.asked = append(p.asked, path+"@"+pin[:8])
	return p.err
}

func compile(t *testing.T, source string) *project.Project {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("fixture: %+v %+v", ds, more)
	}
	return p
}

func TestValidateChecksEachPackFromItsManifest(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	p := compile(t, "cicada 2\ntempo 120\n"+
		fmt.Sprintf("sampler keys {\n  pack = \"packs/keys/manifest.json\"\n  sha256 = %q\n  root = c4\n  voices = 2\n}\n", pin)+
		"track lead keys {}\npattern hit notes steps=4 { c4 . . . }\nscene main { lead = hit }\nsong { main }\n")
	checked := &packs{}
	if err := rules.Validate(p, 48000, 128, checked); err != nil {
		t.Fatalf("a pack its manifest accepts: %v", err)
	}
	if len(checked.asked) != 1 || checked.asked[0] != "packs/keys/manifest.json@abababab" {
		t.Fatalf("packs checked: %v", checked.asked)
	}
	refused := &packs{err: errors.New("instrument manifest hash mismatch")}
	if err := rules.Validate(p, 48000, 128, refused); err != refused.err {
		t.Fatalf("a refused pack: %v", err)
	}
}

func TestValidateRefusesAnAssetOverTheFrameLimit(t *testing.T) {
	// A sampler asset cannot stream, so it must fit the frame limit, and the
	// refusal names that limit rather than the shared budget.
	frames := int64(rules.MaxFrames) + 1
	p := compile(t, fmt.Sprintf("cicada 2\ntempo 120\nasset wave \"wave.wav\" { sha256 = %q format = wav frames = %d rate = 48000Hz channels = 1 }\nsampler hit { asset = wave root = c3 mode = oneshot voices = 2 }\ntrack lead hit {}\npattern melody { c3 . . . }\nscene main { lead = melody }\nsong { main }\n", strings.Repeat("0", 64), frames))
	err := rules.Validate(p, 48000, 128, &packs{})
	want := fmt.Sprintf("asset wave exceeds the resident limit of %d frames (found %d)", int64(rules.MaxFrames), frames)
	if err == nil || err.Error() != want {
		t.Fatalf("over the frame limit: %v, want %q", err, want)
	}
}

func TestValidateRefusesAssetsOverTheResidentBudget(t *testing.T) {
	// Each asset is under the frame limit; together they exceed 64 MiB.
	const frames = 5_000_000
	asset := func(name string) string {
		return fmt.Sprintf("asset %s \"%s.wav\" { sha256 = %q format = wav frames = %d rate = 48000Hz channels = 2 }\n", name, name, strings.Repeat("0", 64), frames)
	}
	p := compile(t, "cicada 2\ntempo 120\n"+asset("one")+asset("two")+
		"sampler hit { asset = one root = c3 mode = oneshot voices = 2 }\nsampler tap { asset = two root = c3 mode = oneshot voices = 2 }\n"+
		"track lead hit {}\ntrack second tap {}\npattern melody { c3 . . . }\nscene main { lead = melody second = melody }\nsong { main }\n")
	err := rules.Validate(p, 48000, 128, &packs{})
	if err == nil || !strings.Contains(err.Error(), "64 MiB resident PCM budget: asset two needs 38.1 MiB on top of 38.1 MiB already resident") {
		t.Fatalf("over the budget: %v", err)
	}
}

func TestFrameLimitAllowsLongerMonoRecordings(t *testing.T) {
	if got := rules.FrameLimit(project.Asset{Source: "recorded", Channels: 1}); got != 16<<20 {
		t.Fatalf("recorded mono limit %d", got)
	}
	if got := rules.FrameLimit(project.Asset{Source: "recorded", Channels: 2}); got != rules.MaxFrames {
		t.Fatalf("recorded stereo limit %d", got)
	}
}
